package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	TCPRoutes        map[string]TCPRoute // category -> raw-TLS entrypoint (legacy SNI fallback)
	Pool             PortPool            // plain-TCP per-instance port pool (preferred for raw TCP)
	ResyncInterval   time.Duration       // status refresh cadence while an instance lives
	CPURequestPct    int64               // pod cpu request as % of its limit (0 = leave to k8s)
	MemRequestPct    int64               // pod memory request as % of its limit (0 = leave to k8s)
	MaxLifetime      time.Duration       // hard cap on any instance's life, extensions included (0 = none)
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
	Reader client.Reader // uncached, for the rare existence re-check
	Scheme *runtime.Scheme
	Cfg    Config
}

// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=instancer.anvil.dev,resources=challengeinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;services;pods;resourcequotas;limitranges;configmaps,verbs=get;list;watch;create;update;patch;delete
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

	// a healthy instance costs a cache read, not a round of creates + a status
	// write; at a thousand instances that resync was hundreds of api writes/s.
	if inst.Status.Phase == "Ready" && len(inst.Status.Endpoints) > 0 {
		phase, err := r.instancePhase(ctx, ns, inst)
		if err != nil {
			return ctrl.Result{}, err
		}
		if phase == "Ready" {
			return ctrl.Result{RequeueAfter: r.requeueAfter(deadline)}, nil
		}
		// a pod went missing or unready (spot preemption, crash): repair below
	}

	if err := r.deleteFailedPods(ctx, ns); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureNamespace(ctx, inst); err != nil {
		return ctrl.Result{}, err
	}

	objs := []client.Object{buildQuota(ns), buildLimitRange(ns)}
	for _, np := range networkPolicies(inst, ns, r.Cfg.TraefikNamespace, r.Cfg.Pool.Namespace) {
		objs = append(objs, np)
	}
	for _, p := range inst.Spec.Pods {
		if p.Egress {
			objs = append(objs, egressPolicy(ns, p.Name))
		}
		if svc := buildService(inst, p); svc != nil {
			objs = append(objs, svc)
		}
		objs = append(objs, buildPod(inst, p, r.Cfg, exposedPods))
	}
	routes, endpoints, err := r.routesAndEndpoints(ctx, inst, ns)
	if err != nil {
		// pool exhausted: don't crash-loop. Mark Pending with a clear reason and
		// retry so a port freed by a reaped instance is picked up automatically.
		if _, exhausted := err.(errPoolExhausted); exhausted {
			lg.Info("tcp port pool exhausted, instance waiting for capacity", "instance", inst.Name)
			if serr := r.setStatus(ctx, inst, "Pending", ns, nil, "waiting for an available port (challenge servers at capacity, retrying)"); serr != nil {
				return ctrl.Result{}, serr
			}
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
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
	if err := r.setStatus(ctx, inst, phase, ns, endpoints, ""); err != nil {
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
	// free the instance's pooled TCP port(s) — the lock objects live in the
	// operator namespace, so they are not GC'd with the instance namespace.
	if err := r.releasePorts(ctx, inst.Name); err != nil {
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
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	// admission (quota) runs before the existence check, so re-creating an object
	// that exists in a full namespace says Forbidden, not AlreadyExists.
	if apierrors.IsForbidden(err) && r.Reader != nil {
		existing := o.DeepCopyObject().(client.Object)
		if gerr := r.Reader.Get(ctx, client.ObjectKeyFromObject(o), existing); gerr == nil {
			return nil
		}
	}
	return err
}

func (r *ChallengeInstanceReconciler) routesAndEndpoints(ctx context.Context, inst *instv1.ChallengeInstance, ns string) ([]client.Object, []instv1.InstanceEndpoint, error) {
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
			// raw TCP: a plain per-instance port from the pool, served by the
			// tcpproxy (NOT Traefik, whose TCP proxy drops the reply on a client
			// half-close and so can never deliver a flag). We allocate the port +
			// record the backend in the lock ConfigMap; the proxy routes on it.
			// Falls back to the legacy Traefik SNI+TLS route only if the pool is
			// unconfigured (dev / pre-migration).
			if r.Cfg.Pool.enabled() {
				backend := fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc, ns, e.ContainerPort)
				port, err := r.allocatePort(ctx, inst, backend)
				if err != nil {
					return nil, nil, err
				}
				h := e.Category + "." + r.Cfg.BaseDomain // web3.h7tex.com / pwn.h7tex.com
				eps = append(eps, instv1.InstanceEndpoint{
					Kind: e.Kind, Host: h, Port: int32(port), Title: e.Title,
					Connect: fmt.Sprintf("nc %s %d", h, port),
				})
			} else {
				tr := r.Cfg.tcpRouteFor(e.Category)
				objs = append(objs, ingressRouteTCP(inst, ns, name, hostname, svc, e.ContainerPort, tr.EntryPoint))
				eps = append(eps, instv1.InstanceEndpoint{
					Kind: e.Kind, Host: hostname, Port: tr.Port, Title: e.Title,
					Connect: fmt.Sprintf("ncat --ssl %s %d", hostname, tr.Port),
				})
			}
		default: // http / https
			objs = append(objs, ingressRoute(inst, ns, name, hostname, svc, httpEP, e.ContainerPort))
			eps = append(eps, instv1.InstanceEndpoint{
				Kind: e.Kind, Host: hostname, Port: httpPort, Title: e.Title,
				Connect: "https://" + hostname,
			})
		}
	}
	return objs, eps, nil
}

// deleteFailedPods clears pods that can't come back on their own (evicted, or
// killed with their node), so the create pass replaces them.
func (r *ChallengeInstanceReconciler) deleteFailedPods(ctx context.Context, ns string) error {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{labelManaged: "true"}); err != nil {
		return err
	}
	for i := range pods.Items {
		if pods.Items[i].Status.Phase != corev1.PodFailed {
			continue
		}
		if err := r.Delete(ctx, &pods.Items[i]); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
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

func (r *ChallengeInstanceReconciler) setStatus(ctx context.Context, inst *instv1.ChallengeInstance, phase, ns string, eps []instv1.InstanceEndpoint, msg string) error {
	orig := inst.DeepCopy()
	inst.Status.Phase = phase
	inst.Status.Namespace = ns
	inst.Status.Endpoints = eps
	inst.Status.ObservedGeneration = inst.Generation
	if msg == "" {
		msg = "instance " + phase
	}
	meta := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             phase,
		Message:            msg,
		ObservedGeneration: inst.Generation,
	}
	if phase == "Ready" {
		meta.Status = metav1.ConditionTrue
	}
	setCondition(&inst.Status.Conditions, meta)
	if equality.Semantic.DeepEqual(orig.Status, inst.Status) {
		return nil
	}
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
		if hardCap := inst.CreationTimestamp.Add(inst.Spec.MaxLifetime.Duration); hardCap.Before(exp) {
			exp = hardCap
		}
	}
	// cluster-wide backstop: a bad expiry (a seconds/minutes mixup made instances
	// live 30h) must not pin ports and nodes for the whole event.
	if r.Cfg.MaxLifetime > 0 {
		if hardCap := inst.CreationTimestamp.Add(r.Cfg.MaxLifetime); hardCap.Before(exp) {
			exp = hardCap
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
	// pods are not owned by the cluster-scoped CR, so map them back by label: a
	// preempted or crashed pod gets repaired now instead of at the next resync.
	podToInstance := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
		name := o.GetLabels()[labelInstance]
		if name == "" || o.GetLabels()[labelManaged] != "true" {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name}}}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&instv1.ChallengeInstance{}).
		Owns(&corev1.Namespace{}).
		Watches(&corev1.Pod{}, podToInstance).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
