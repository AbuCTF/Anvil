// Package instancer drives the GKE ChallengeInstance operator from the Anvil
// API. It creates/deletes cluster-scoped ChallengeInstance CRs via the dynamic
// client, so the main binary never imports controller-runtime. The per-team
// instance id is HMAC-derived; the operator allocates the plain-TCP port and
// publishes the connect endpoints in the CR status, which the API reads back.
package instancer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"go.uber.org/zap"

	"github.com/anvil-lab/anvil/internal/config"
)

var gvr = schema.GroupVersionResource{Group: "instancer.anvil.dev", Version: "v1alpha1", Resource: "challengeinstances"}

// Service talks to the ChallengeInstance CRD. When the backend is not "k8s" (or
// we are not running in-cluster) it is inert and Enabled() reports false.
type Service struct {
	dyn    dynamic.Interface
	cfg    config.InstancerConfig
	logger *zap.Logger
}

// PortSpec is one exposed challenge port. Service ("http"|"https"|"tcp") is the
// explicit per-port exposure — it alone decides web-vs-raw routing, never the
// challenge category (a misc challenge can be pure TCP).
type PortSpec struct {
	Port     int
	Protocol string
	Service  string
}

// LaunchSpec is one instance to spawn. Single-container challenges set
// Image/Tag/Ports/Flags; multi-container (compose-style) challenges set
// Containers instead — one pod per role, each with its own already-resolved env.
type LaunchSpec struct {
	TeamID      string
	ChallengeID string
	Slug        string
	Image       string // registry already folded in by the importer
	Tag         string
	CPULimit    string
	MemoryLimit string
	Ports       []PortSpec
	Flags       map[string]string
	Containers  []ContainerSpec // non-empty => multi-container; supersedes Image/Ports/Flags
	Timeout     time.Duration
	Privesc     bool // single-container: relax securityContext (allowPrivilegeEscalation:true / no_new_privs off)
}

// ContainerSpec is one role in a multi-container challenge. It becomes one pod
// (named Name) with a ClusterIP service named Name, so peers resolve it by that
// name (e.g. http://scanner:8081). Env is fully resolved by the API (per-instance
// placeholders already substituted), so secrets like FLAG are scoped to the
// single role that declared them — never broadcast.
type ContainerSpec struct {
	Name        string
	Image       string // defaults to LaunchSpec.Image when empty
	Tag         string
	Command     []string // compose command -> k8s container args (keeps the image ENTRYPOINT)
	Env         map[string]string
	Ports       []PortSpec // internal listen ports; each role with ports gets a ClusterIP service
	Public      bool       // only public roles get a route/expose entry
	Egress      bool
	Privesc     bool // relax this role's securityContext (allowPrivilegeEscalation:true / no_new_privs off)
	CPULimit    string
	MemoryLimit string
}

// Endpoint is a player-facing connection to a launched instance.
type Endpoint struct {
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Connect string `json:"connect"`
}

// LaunchResult is what the API records + returns.
type LaunchResult struct {
	InstanceID string
	Namespace  string
	Endpoints  []Endpoint
}

// NewService builds the client when backend=k8s and we can reach the apiserver
// in-cluster; otherwise it degrades to an inert service so the docker path and
// local dev are unaffected.
func NewService(cfg config.InstancerConfig, logger *zap.Logger) (*Service, error) {
	s := &Service{cfg: cfg, logger: logger}
	if cfg.Backend != "k8s" {
		return s, nil
	}
	rc, err := rest.InClusterConfig()
	if err != nil {
		logger.Warn("instancer backend is k8s but not running in-cluster; instancing will error until deployed on GKE", zap.Error(err))
		return s, nil
	}
	// client-go defaults to 5 qps / burst 10 per pod, which queues a launch
	// wave behind itself; the apiserver's own fairness is the real limit.
	rc.QPS, rc.Burst = 100, 200
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	s.dyn = dyn
	logger.Info("instancer service ready", zap.String("base_domain", cfg.BaseDomain))
	return s, nil
}

// Enabled reports whether the k8s backend is active and connected.
func (s *Service) Enabled() bool { return s != nil && s.dyn != nil }

// InstanceID is the deterministic per-team instance id (matches the operator).
func (s *Service) InstanceID(teamID, challengeID string) string {
	m := hmac.New(sha256.New, []byte(s.cfg.HMACSecret))
	m.Write([]byte(teamID))
	m.Write([]byte{0})
	m.Write([]byte(challengeID))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// Launch creates (idempotently) the ChallengeInstance CR, then waits for the
// operator to allocate the instance's port(s) and publish endpoints in the CR
// status. Raw-TCP challenges get a port from the plain-TCP pool, chosen by the
// controller, so the connect string is NOT known until the first reconcile: we
// read it back from the status rather than guess, keeping the API and the
// operator from drifting.
func (s *Service) Launch(ctx context.Context, spec LaunchSpec) (*LaunchResult, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("instancer k8s backend not available")
	}
	id := s.InstanceID(spec.TeamID, spec.ChallengeID)
	var expose []map[string]any
	if len(spec.Containers) > 0 {
		expose = buildExposeMulti(spec.Containers)
	} else {
		expose = s.buildExpose(spec.Ports)
	}
	cr := s.buildCR(id, spec, expose)

	if err := s.create(ctx, cr); err != nil {
		return nil, err
	}

	eps, err := s.waitForEndpoints(ctx, id)
	if err != nil {
		// never leave a half-born instance running unaccounted; a retry recreates it
		dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if derr := s.Destroy(dctx, id); derr != nil {
			s.logger.Warn("failed to clean up instance after launch failure", zap.String("instance", id), zap.Error(derr))
		}
		return nil, err
	}
	return &LaunchResult{InstanceID: id, Namespace: "inst-" + id, Endpoints: eps}, nil
}

// create makes the CR. The id is deterministic per team+challenge, so a
// relaunch right after a stop can collide with the previous incarnation still
// in its finalizer; wait that one out instead of adopting its dying endpoints.
func (s *Service) create(ctx context.Context, cr *unstructured.Unstructured) error {
	deadline := time.Now().Add(45 * time.Second)
	for {
		_, err := s.dyn.Resource(gvr).Create(ctx, cr, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ChallengeInstance: %w", err)
		}
		old, gerr := s.dyn.Resource(gvr).Get(ctx, cr.GetName(), metav1.GetOptions{})
		if gerr == nil && old.GetDeletionTimestamp() == nil {
			return nil // live instance already exists, reuse it
		}
		if gerr != nil && !apierrors.IsNotFound(gerr) {
			return fmt.Errorf("read existing ChallengeInstance: %w", gerr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("previous instance is still shutting down, please try again shortly")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Extend moves the CR's expiry so the operator's reaper honours an extension.
func (s *Service) Extend(ctx context.Context, instanceID string, expiresAt time.Time) error {
	if !s.Enabled() {
		return fmt.Errorf("instancer k8s backend not available")
	}
	patch := fmt.Sprintf(`{"spec":{"expiresAt":%q}}`, expiresAt.UTC().Format(time.RFC3339))
	_, err := s.dyn.Resource(gvr).Patch(ctx, instanceID, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// waitForEndpoints polls the CR status until the operator publishes endpoints
// (port allocated + routes programmed, within ~1-2s and independent of pod
// readiness) or the instance errors / the wait times out. A timeout means the
// instance never came up (pool at capacity, image pull stuck, controller down),
// surfaced to the caller as a clean, retryable error.
func (s *Service) waitForEndpoints(ctx context.Context, id string) ([]Endpoint, error) {
	deadline := time.Now().Add(30 * time.Second)
	lastPhase := ""
	for {
		st, err := s.Status(ctx, id)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("read instance status: %w", err)
		}
		if st != nil {
			lastPhase = st.Phase
			if len(st.Endpoints) > 0 {
				return st.Endpoints, nil
			}
			if st.Phase == "Errored" {
				return nil, fmt.Errorf("instance failed to provision")
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("instance did not become reachable in time (phase %q): it may be at capacity, please try again shortly", lastPhase)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// InstanceStatus is a snapshot of the operator-published CR status.
type InstanceStatus struct {
	Phase     string
	Namespace string
	Endpoints []Endpoint
}

// Status reads the operator-published status (phase + endpoints) for an instance.
func (s *Service) Status(ctx context.Context, id string) (*InstanceStatus, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("instancer k8s backend not available")
	}
	obj, err := s.dyn.Resource(gvr).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := &InstanceStatus{}
	out.Phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
	out.Namespace, _, _ = unstructured.NestedString(obj.Object, "status", "namespace")
	if raw, found, _ := unstructured.NestedSlice(obj.Object, "status", "endpoints"); found {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			ep := Endpoint{Port: coerceInt(m, "port")}
			ep.Kind, _, _ = unstructured.NestedString(m, "kind")
			ep.Host, _, _ = unstructured.NestedString(m, "host")
			ep.Connect, _, _ = unstructured.NestedString(m, "connect")
			out.Endpoints = append(out.Endpoints, ep)
		}
	}
	return out, nil
}

// coerceInt reads a numeric map value regardless of how the dynamic client
// decoded it (int64 from the apiserver, float64 from a JSON round-trip).
func coerceInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// Destroy deletes the instance CR; the operator's finalizer tears down the rest.
func (s *Service) Destroy(ctx context.Context, instanceID string) error {
	if !s.Enabled() {
		return fmt.Errorf("instancer k8s backend not available")
	}
	err := s.dyn.Resource(gvr).Delete(ctx, instanceID, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// buildExpose turns exposed ports into CRD expose entries. The player-facing
// endpoints (host + port + connect string) are NOT computed here: the operator
// owns them (it allocates the pool port and programs Traefik), so the API reads
// them back from the CR status instead of duplicating the logic.
func (s *Service) buildExpose(ports []PortSpec) []map[string]any {
	var expose []map[string]any
	seen := map[string]int{}
	for _, p := range ports {
		kind := exposeKind(p.Service)
		class := routingClass(p.Service, kind) // web (http/https) | web3 | pwn (tcp)
		prefix := class
		if n := seen[class]; n > 0 {
			prefix = fmt.Sprintf("%s%d", class, n)
		}
		seen[class]++

		expose = append(expose, map[string]any{
			"kind":          kind,
			"hostPrefix":    prefix,
			"containerName": "main",
			"containerPort": int64(p.Port),
			"category":      class,
		})
	}
	return expose
}

// buildExposeMulti emits expose entries only for the public roles of a
// multi-container challenge; containerName is the role/pod name so the operator
// routes to that pod's service. Internal-only roles (no Public) get no route.
func buildExposeMulti(containers []ContainerSpec) []map[string]any {
	var expose []map[string]any
	seen := map[string]int{}
	for _, c := range containers {
		if !c.Public {
			continue
		}
		for _, p := range c.Ports {
			kind := exposeKind(p.Service)
			class := routingClass(p.Service, kind)
			prefix := class
			if n := seen[class]; n > 0 {
				prefix = fmt.Sprintf("%s%d", class, n)
			}
			seen[class]++
			expose = append(expose, map[string]any{
				"kind":          kind,
				"hostPrefix":    prefix,
				"containerName": c.Name,
				"containerPort": int64(p.Port),
				"category":      class,
			})
		}
	}
	return expose
}

// containerPod builds one CRD pod (a single container + its ClusterIP service).
// The service is named after the pod, so peers resolve it by that name.
func containerPod(name, image string, args []string, ports []PortSpec, env map[string]string, cpu, mem string, egress, privesc bool) map[string]any {
	var containerPorts, svcPorts []any
	for _, p := range ports {
		containerPorts = append(containerPorts, map[string]any{"containerPort": int64(p.Port)})
		svcPorts = append(svcPorts, map[string]any{"port": int64(p.Port), "targetPort": int64(p.Port)})
	}
	var envList []any
	for k, v := range env {
		envList = append(envList, map[string]any{"name": k, "value": v})
	}
	container := map[string]any{"name": name, "image": image}
	if len(args) > 0 {
		as := make([]any, len(args))
		for i, a := range args {
			as[i] = a
		}
		container["args"] = as // compose command -> k8s args (keeps the image ENTRYPOINT)
	}
	if len(containerPorts) > 0 {
		container["ports"] = containerPorts
	}
	if len(envList) > 0 {
		container["env"] = envList
	}
	if lim := resourceLimits(cpu, mem); lim != nil {
		container["resources"] = map[string]any{"limits": lim}
	}
	if privesc {
		// opt-in (boot-to-root / SUID challenges): no_new_privs off so setuid works.
		// The operator's hardenContainer only defaults a NIL securityContext, so caps
		// stay dropped-ALL and gVisor + default-deny egress are untouched.
		container["securityContext"] = map[string]any{"allowPrivilegeEscalation": true}
	}
	pod := map[string]any{
		"name": name,
		"spec": map[string]any{"containers": []any{container}},
	}
	if len(svcPorts) > 0 {
		pod["ports"] = svcPorts
	}
	if egress {
		pod["egress"] = true
	}
	return pod
}

// buildPods returns the CRD pod list for either a single-container challenge
// (one "main" pod) or a multi-container one (one pod per role).
func buildPods(spec LaunchSpec) []any {
	if len(spec.Containers) == 0 {
		image := spec.Image
		if spec.Tag != "" {
			image += ":" + spec.Tag
		}
		return []any{containerPod("main", image, nil, spec.Ports, spec.Flags, spec.CPULimit, spec.MemoryLimit, false, spec.Privesc)}
	}
	var pods []any
	for _, c := range spec.Containers {
		image := c.Image
		if image == "" {
			image = spec.Image
		}
		tag := c.Tag
		if tag == "" {
			tag = spec.Tag
		}
		if tag != "" {
			image += ":" + tag
		}
		cpu := c.CPULimit
		if cpu == "" {
			cpu = spec.CPULimit
		}
		mem := c.MemoryLimit
		if mem == "" {
			mem = spec.MemoryLimit
		}
		pods = append(pods, containerPod(c.Name, image, c.Command, c.Ports, c.Env, cpu, mem, c.Egress, c.Privesc))
	}
	return pods
}

func (s *Service) buildCR(id string, spec LaunchSpec, expose []map[string]any) *unstructured.Unstructured {
	pods := buildPods(spec)

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = time.Hour
	}
	cr := &unstructured.Unstructured{}
	cr.SetUnstructuredContent(map[string]any{
		"apiVersion": "instancer.anvil.dev/v1alpha1",
		"kind":       "ChallengeInstance",
		"metadata": map[string]any{
			"name": id,
			"labels": map[string]any{
				"instancer.anvil.dev/team":      spec.TeamID,
				"instancer.anvil.dev/challenge": spec.ChallengeID,
			},
		},
		"spec": map[string]any{
			"teamId":      spec.TeamID,
			"challengeId": spec.ChallengeID,
			"expiresAt":   time.Now().Add(timeout).UTC().Format(time.RFC3339),
			"pods":        pods,
			"expose":      toAnySlice(expose),
		},
	})
	return cr
}

func exposeKind(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "http":
		return "http"
	case "https":
		return "https"
	default:
		return "tcp-ssl"
	}
}

// routingClass maps a service to its shared subdomain + entrypoint: web
// (http/https on :443), web3 (blockchain, raw TLS on the :1337 SNI entrypoint,
// its own subdomain so hosts don't read "pwn"), or pwn (other raw TLS on :1337).
func routingClass(service, kind string) string {
	if strings.ToLower(strings.TrimSpace(service)) == "web3" {
		return "web3"
	}
	if kind == "tcp-ssl" {
		return "pwn"
	}
	return "web"
}

func resourceLimits(cpu, mem string) map[string]any {
	lim := map[string]any{}
	if strings.TrimSpace(cpu) != "" {
		lim["cpu"] = cpu
	}
	if strings.TrimSpace(mem) != "" {
		lim["memory"] = mem
	}
	if len(lim) == 0 {
		return nil
	}
	return lim
}

func toAnySlice(m []map[string]any) []any {
	out := make([]any, len(m))
	for i, e := range m {
		out[i] = e
	}
	return out
}
