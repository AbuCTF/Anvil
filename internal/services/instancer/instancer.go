// Package instancer drives the GKE ChallengeInstance operator from the Anvil
// API. It creates/deletes cluster-scoped ChallengeInstance CRs via the dynamic
// client, so the main binary never imports controller-runtime. The per-team
// instance id (and thus its hostname) is HMAC-derived, so the URL is known the
// instant the CR is created — before the pod is even scheduled.
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

// LaunchSpec is one instance to spawn.
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
	Timeout     time.Duration
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

// Launch creates (idempotently) the ChallengeInstance CR and returns the
// deterministic endpoints. Endpoints are known immediately; the pod becomes
// reachable a few seconds later once the operator schedules it.
func (s *Service) Launch(ctx context.Context, spec LaunchSpec) (*LaunchResult, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("instancer k8s backend not available")
	}
	id := s.InstanceID(spec.TeamID, spec.ChallengeID)
	exposeSpec, endpoints := s.mapPorts(id, spec.Ports)
	cr := s.buildCR(id, spec, exposeSpec)

	_, err := s.dyn.Resource(gvr).Create(ctx, cr, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create ChallengeInstance: %w", err)
	}
	return &LaunchResult{InstanceID: id, Namespace: "inst-" + id, Endpoints: endpoints}, nil
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

// mapPorts turns exposed ports into CRD expose entries + player endpoints,
// keeping the hostPrefix identical on both sides so the returned URL matches
// what the operator programs into Traefik.
func (s *Service) mapPorts(id string, ports []PortSpec) ([]map[string]any, []Endpoint) {
	var expose []map[string]any
	var eps []Endpoint
	seen := map[string]int{}
	for _, p := range ports {
		kind := exposeKind(p.Service)
		class := routingClass(kind) // web (http/https) | pwn (tcp)
		prefix := class
		if n := seen[class]; n > 0 {
			prefix = fmt.Sprintf("%s%d", class, n)
		}
		seen[class]++
		host := fmt.Sprintf("%s-%s.%s.%s", prefix, id, class, s.cfg.BaseDomain)

		expose = append(expose, map[string]any{
			"kind":          kind,
			"hostPrefix":    prefix,
			"containerName": "main",
			"containerPort": int64(p.Port),
			"category":      class,
		})
		if kind == "tcp-ssl" {
			eps = append(eps, Endpoint{Kind: kind, Host: host, Port: 1337, Connect: fmt.Sprintf("ncat --ssl %s 1337", host)})
		} else {
			eps = append(eps, Endpoint{Kind: kind, Host: host, Port: 443, Connect: "https://" + host})
		}
	}
	return expose, eps
}

func (s *Service) buildCR(id string, spec LaunchSpec, expose []map[string]any) *unstructured.Unstructured {
	image := spec.Image
	if spec.Tag != "" {
		image = image + ":" + spec.Tag
	}

	var containerPorts []any
	var svcPorts []any
	for _, p := range spec.Ports {
		containerPorts = append(containerPorts, map[string]any{"containerPort": int64(p.Port)})
		svcPorts = append(svcPorts, map[string]any{"port": int64(p.Port), "targetPort": int64(p.Port)})
	}

	var env []any
	for k, v := range spec.Flags {
		env = append(env, map[string]any{"name": k, "value": v})
	}

	container := map[string]any{"name": "main", "image": image}
	if len(containerPorts) > 0 {
		container["ports"] = containerPorts
	}
	if len(env) > 0 {
		container["env"] = env
	}
	if lim := resourceLimits(spec.CPULimit, spec.MemoryLimit); lim != nil {
		container["resources"] = map[string]any{"limits": lim}
	}

	pod := map[string]any{
		"name": "main",
		"spec": map[string]any{"containers": []any{container}},
	}
	if len(svcPorts) > 0 {
		pod["ports"] = svcPorts
	}

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
			"pods":        []any{pod},
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

// routingClass maps an expose kind to its shared subdomain + entrypoint: web
// (http/https on :443) or pwn (raw TLS on the :1337 SNI entrypoint).
func routingClass(kind string) string {
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
