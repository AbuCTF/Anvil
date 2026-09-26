package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	instv1 "github.com/anvil-lab/anvil/instancer/api/v1alpha1"
	"github.com/anvil-lab/anvil/instancer/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(instv1.AddToScheme(scheme))
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil && v >= 0 {
		return v
	}
	return def
}

// parsePool reads "start-end" (e.g. "30000-30063") into a PortPool. An empty or
// malformed range yields a disabled pool (Start==0), so the controller falls
// back to the legacy SNI route.
func parsePool(rng, ns string) controller.PortPool {
	start, endStr, ok := strings.Cut(strings.TrimSpace(rng), "-")
	if !ok {
		return controller.PortPool{}
	}
	s, err1 := strconv.Atoi(strings.TrimSpace(start))
	e, err2 := strconv.Atoi(strings.TrimSpace(endStr))
	if err1 != nil || err2 != nil {
		return controller.PortPool{}
	}
	return controller.PortPool{Start: s, End: e, Namespace: ns}
}

// parseTCPRoutes reads "cat=entrypoint:port,cat2=ep2:port2" into the config map.
func parseTCPRoutes(s string) map[string]controller.TCPRoute {
	out := map[string]controller.TCPRoute{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cat, epPort, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		ep, portStr, ok := strings.Cut(epPort, ":")
		if !ok {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(cat)] = controller.TCPRoute{EntryPoint: strings.TrimSpace(ep), Port: int32(port)}
	}
	return out
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	lg := ctrl.Log.WithName("setup")

	resync, _ := time.ParseDuration(env("INSTANCER_RESYNC", "30s"))
	maxLife, _ := time.ParseDuration(env("INSTANCER_MAX_LIFETIME", "0"))
	httpPort, _ := strconv.Atoi(env("INSTANCER_HTTP_PORT", "443"))
	cfg := controller.Config{
		BaseDomain:            env("INSTANCER_BASE_DOMAIN", "h7tex.com"),
		RuntimeClass:          env("INSTANCER_RUNTIME_CLASS", "gvisor"),
		TraefikNamespace:      env("INSTANCER_TRAEFIK_NAMESPACE", "traefik"),
		HTTPEntryPoint:        env("INSTANCER_HTTP_ENTRYPOINT", "websecure"),
		HTTPPort:              int32(httpPort),
		TCPRoutes:             parseTCPRoutes(env("INSTANCER_TCP_ROUTES", "pwn=pwn:1337")),
		Pool:                  parsePool(env("INSTANCER_TCP_POOL_RANGE", ""), env("INSTANCER_TCP_POOL_NAMESPACE", "anvil-instancer")),
		ResyncInterval:        resync,
		CPURequestPct:         envInt("INSTANCER_CPU_REQUEST_PCT", 10),
		MemRequestPct:         envInt("INSTANCER_MEM_REQUEST_PCT", 25),
		MaxLifetime:           maxLife,
		ControlPlaneNamespace: env("INSTANCER_CONTROL_PLANE_NAMESPACE", "anvil"),
		ControlPlanePort:      int32(envInt("INSTANCER_CONTROL_PLANE_PORT", 8080)),
	}

	maxConcurrent := 8
	if v, err := strconv.Atoi(os.Getenv("INSTANCER_MAX_CONCURRENT")); err == nil && v > 0 {
		maxConcurrent = v
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: env("INSTANCER_METRICS_ADDR", ":8080")},
		HealthProbeBindAddress: env("INSTANCER_PROBE_ADDR", ":8081"),
		LeaderElection:         false,
	})
	if err != nil {
		lg.Error(err, "unable to start manager")
		os.Exit(1)
	}

	r := &controller.ChallengeInstanceReconciler{Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), Scheme: mgr.GetScheme(), Cfg: cfg}
	if err := r.SetupWithManager(mgr, maxConcurrent); err != nil {
		lg.Error(err, "unable to create controller")
		os.Exit(1)
	}

	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	lg.Info("starting instancer", "baseDomain", cfg.BaseDomain, "runtimeClass", cfg.RuntimeClass)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		lg.Error(err, "manager exited")
		os.Exit(1)
	}
}
