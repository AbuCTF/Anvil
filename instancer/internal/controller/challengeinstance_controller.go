package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	instv1 "github.com/anvil-lab/anvil/instancer/api/v1alpha1"
)

// TCPRoute is the Traefik TCP entrypoint (name + external port) that carries a
// category's raw-TLS challenges. These must exist in Traefik's static config.
type TCPRoute struct {
	EntryPoint string
	Port       int32
}

// Config holds cluster-wide routing and isolation settings.
type Config struct {
	BaseDomain       string              // e.g. h7tex.com
	RuntimeClass     string              // e.g. gvisor; empty disables
	TraefikNamespace string              // namespace Traefik runs in
	HTTPEntryPoint   string              // Traefik entrypoint for http/https (e.g. websecure)
	HTTPPort         int32               // external port players reach http/https on (443)
	TCPRoutes        map[string]TCPRoute // category -> raw-TLS entrypoint
	ResyncInterval   time.Duration       // status refresh cadence while an instance lives
}

// tcpRouteFor returns the TCP entrypoint for a category, defaulting the
// entrypoint name to the category itself on port 1337 when unmapped.
func (c Config) tcpRouteFor(category string) TCPRoute {
	if r, ok := c.TCPRoutes[category]; ok {
		return r
	}
	return TCPRoute{EntryPoint: category, Port: 1337}
}

// ChallengeInstanceReconciler drives one instance to its desired state.
type ChallengeInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cfg    Config
}

// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;services;pods;resourcequotas;limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=traefik.io,resources=ingressroutes;ingressroutetcps,verbs=get;list;watch;create;update;patch;delete

func (r *ChallengeInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	inst := &instv1.ChallengeInstance{}
	if err := r.Get(ctx, req.NamespacedName, inst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !inst.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, inst)
	}

	if !controllerutil.ContainsFinalizer(inst, finalizer) {
		patch := client.MergeFrom(inst.DeepCopy())
		controllerutil.AddFinalizer(inst, finalizer)
		if err := r.Patch(ctx, inst, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reaper: past the effective deadline, delete self; the deletion path tears down.
	deadline := r.effectiveExpiry(inst)
	if !time.Now().Before(deadline) {
		lg.Info("instance expired, deleting", "deadline", deadline)
		return ctrl.Result{}, r.Delete(ctx, inst)
	}

	ns := namespaceFor(inst.Name)
	exposedPods := exposedPodSet(inst)

	if err := r.ensureNamespace(ctx, inst); err != nil {
		return ctrl.Result{}, err
	}

	objs := []client.Object{buildQuota(ns), buildLimitRange(ns)}
	for _, np := range networkPolicies(inst, ns, r.Cfg.TraefikNamespace) {
		objs = append(objs, np)
	}
	for _, p := range inst.Spec.Pods {
		if p.Egress {
			objs = append(objs, egressPolicy(ns, p.Name))
		}
		if svc := buildService(inst, p); svc != nil {
			objs = append(objs, svc)
		}
		objs = append(objs, buildPod(inst, p, r.Cfg.RuntimeClass, exposedPods))
	}
	routes, endpoints := r.routesAndEndpoints(inst, ns)
	objs = append(objs, routes...)

	for _, o := range objs {
		if err := r.createIfAbsent(ctx, o); err != nil {
			return ctrl.Result{}, fmt.Errorf("create %T %s: %w", o, o.GetName(), err)
		}
	}

	phase, err := r.instancePhase(ctx, ns, inst)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.setStatus(ctx, inst, phase, ns, endpoints); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.requeueAfter(deadline)}, nil
}

func (r *ChallengeInstanceReconciler) finalize(ctx context.Context, inst *instv1.ChallengeInstance) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(inst, finalizer) {
		return ctrl.Result{}, nil
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceFor(inst.Name)}}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	// Wait for the namespace to be gone before dropping the finalizer, so cleanup is complete.
	err := r.Get(ctx, types.NamespacedName{Name: ns.Name}, &corev1.Namespace{})
	if err == nil {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	patch := client.MergeFrom(inst.DeepCopy())
	controllerutil.RemoveFinalizer(inst, finalizer)
	return ctrl.Result{}, r.Patch(ctx, inst, patch)
}

func (r *ChallengeInstanceReconciler) ensureNamespace(ctx context.Context, inst *instv1.ChallengeInstance) error {
	ns := buildNamespace(inst)
	if err := controllerutil.SetControllerReference(inst, ns, r.Scheme); err != nil {
		return err
	}
	return r.createIfAbsent(ctx, ns)
}

// createIfAbsent creates an object once. Instances are immutable while alive
// (hot-update is a later phase), so there is no drift to reconcile.
func (r *ChallengeInstanceReconciler) createIfAbsent(ctx context.Context, o client.Object) error {
	err := r.Create(ctx, o)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (r *ChallengeInstanceReconciler) routesAndEndpoints(inst *instv1.ChallengeInstance, ns string) ([]client.Object, []instv1.InstanceEndpoint) {
	var objs []client.Object
	var eps []instv1.InstanceEndpoint
	httpEP := r.Cfg.HTTPEntryPoint
	if httpEP == "" {
		httpEP = "websecure"
	}
	httpPort := r.Cfg.HTTPPort
	if httpPort == 0 {
		httpPort = 443
	}
	for i, e := range inst.Spec.Expose {
		hostname := host(e.HostPrefix, inst.Name, e.Category, r.Cfg.BaseDomain)
		name := fmt.Sprintf("route-%d-%s", i, e.HostPrefix)
		svc := serviceName(e.ContainerName)
		switch e.Kind {
		case instv1.ExposeTCPSSL:
			tr := r.Cfg.tcpRouteFor(e.Category)
			objs = append(objs, ingressRouteTCP(inst, ns, name, hostname, svc, e.ContainerPort, tr.EntryPoint))
			eps = append(eps, instv1.InstanceEndpoint{
				Kind: e.Kind, Host: hostname, Port: tr.Port, Title: e.Title,
				Connect: fmt.Sprintf("ncat --ssl %s %d", hostname, tr.Port),
			})
		default: // http / https
			objs = append(objs, ingressRoute(inst, ns, name, hostname, svc, httpEP, e.ContainerPort))
			eps = append(eps, instv1.InstanceEndpoint{
				Kind: e.Kind, Host: hostname, Port: httpPort, Title: e.Title,
				Connect: "https://" + hostname,
			})
		}
	}
	return objs, eps
}

func (r *ChallengeInstanceReconciler) instancePhase(ctx context.Context, ns string, inst *instv1.ChallengeInstance) (string, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{labelManaged: "true"}); err != nil {
		return "", err
	}
	if len(pods.Items) < len(inst.Spec.Pods) {
		return "Provisioning", nil
	}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodFailed {
			return "Errored", nil
		}
		ready := false
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready {
			return "Provisioning", nil
		}
	}
	return "Ready", nil
}

func (r *ChallengeInstanceReconciler) setStatus(ctx context.Context, inst *instv1.ChallengeInstance, phase, ns string, eps []instv1.InstanceEndpoint) error {
	orig := inst.DeepCopy()
	inst.Status.Phase = phase
	inst.Status.Namespace = ns
	inst.Status.Endpoints = eps
	inst.Status.ObservedGeneration = inst.Generation
	meta := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             phase,
		Message:            "instance " + phase,
		ObservedGeneration: inst.Generation,
	}
	if phase == "Ready" {
		meta.Status = metav1.ConditionTrue
	}
	setCondition(&inst.Status.Conditions, meta)
	return r.Status().Patch(ctx, inst, client.MergeFrom(orig))
}

func setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	c.LastTransitionTime = metav1.Now()
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			if (*conds)[i].Status == c.Status {
				c.LastTransitionTime = (*conds)[i].LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

func (r *ChallengeInstanceReconciler) effectiveExpiry(inst *instv1.ChallengeInstance) time.Time {
	exp := inst.Spec.ExpiresAt.Time
	if inst.Spec.MaxLifetime != nil {
		hardCap := inst.CreationTimestamp.Add(inst.Spec.MaxLifetime.Duration)
		if hardCap.Before(exp) {
			return hardCap
		}
	}
	return exp
}

func (r *ChallengeInstanceReconciler) requeueAfter(deadline time.Time) time.Duration {
	until := time.Until(deadline)
	if until <= 0 {
		return time.Second
	}
	if r.Cfg.ResyncInterval > 0 && r.Cfg.ResyncInterval < until {
		return r.Cfg.ResyncInterval
	}
	return until
}

func exposedPodSet(inst *instv1.ChallengeInstance) map[string]bool {
	m := map[string]bool{}
	for _, e := range inst.Spec.Expose {
		m[e.ContainerName] = true
	}
	return m
}

func (r *ChallengeInstanceReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrent int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&instv1.ChallengeInstance{}).
		Owns(&corev1.Namespace{}).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
