package instancer

import (
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
)

func newTestSvc() *Service {
	return &Service{cfg: config.InstancerConfig{HMACSecret: "secret", BaseDomain: "h7tex.com"}}
}

func TestInstanceIDDeterministic(t *testing.T) {
	s := newTestSvc()
	a := s.InstanceID("team1", "chalA")
	if len(a) != 16 {
		t.Fatalf("want 16 hex chars, got %d (%q)", len(a), a)
	}
	if a != s.InstanceID("team1", "chalA") {
		t.Fatal("not deterministic")
	}
	if a == s.InstanceID("team2", "chalA") {
		t.Fatal("different team must differ")
	}
}

func TestExposeKindAndRouting(t *testing.T) {
	// an unset/unknown service is treated as raw TLS, never mis-served as http.
	if exposeKind("") != "tcp-ssl" || exposeKind("weird") != "tcp-ssl" {
		t.Fatal("unknown service must default to tcp-ssl")
	}
	if exposeKind("https") != "https" || routingClass("https", "https") != "web" {
		t.Fatal("https must route as web")
	}
	if routingClass("web3", exposeKind("web3")) != "web3" {
		t.Fatal("web3 service must route as web3")
	}
	if routingClass("tcp", "tcp-ssl") != "pwn" {
		t.Fatal("plain tcp must stay pwn")
	}
}

// helpers to dig into the unstructured pod/expose maps.
func containersOf(pod any) []any {
	spec := pod.(map[string]any)["spec"].(map[string]any)
	return spec["containers"].([]any)
}
func envOf(container any) map[string]string {
	out := map[string]string{}
	raw, ok := container.(map[string]any)["env"].([]any)
	if !ok {
		return out
	}
	for _, e := range raw {
		m := e.(map[string]any)
		out[m["name"].(string)] = m["value"].(string)
	}
	return out
}

func TestBuildPodsSingle(t *testing.T) {
	pods := buildPods(LaunchSpec{
		Image: "img", Tag: "v1",
		Ports: []PortSpec{{Port: 80, Service: "http"}},
		Flags: map[string]string{"FLAG": "H7CTF{x}"},
	})
	if len(pods) != 1 {
		t.Fatalf("single-container must yield one pod, got %d", len(pods))
	}
	p := pods[0].(map[string]any)
	if p["name"] != "main" {
		t.Fatalf("single pod name must be main, got %v", p["name"])
	}
	c := containersOf(pods[0])[0]
	if c.(map[string]any)["image"] != "img:v1" {
		t.Fatalf("image tag not folded: %v", c.(map[string]any)["image"])
	}
	if envOf(c)["FLAG"] != "H7CTF{x}" {
		t.Fatal("single-container FLAG must be injected into main")
	}
}

func TestBuildExposeMultiOnlyPublic(t *testing.T) {
	expose := buildExposeMulti([]ContainerSpec{
		{Name: "portal", Public: true, Ports: []PortSpec{{Port: 8080, Service: "http"}}},
		{Name: "scanner", Ports: []PortSpec{{Port: 8081, Service: "http"}}},
	})
	if len(expose) != 1 {
		t.Fatalf("only the public role should be exposed, got %d entries", len(expose))
	}
	if expose[0]["containerName"] != "portal" {
		t.Fatalf("expose must target the public role, got %v", expose[0]["containerName"])
	}
}

func TestBuildPodsMultiFlagScoping(t *testing.T) {
	pods := buildPods(LaunchSpec{
		Image: "base",
		Containers: []ContainerSpec{
			{Name: "portal", Command: []string{"portal"}, Public: true,
				Ports: []PortSpec{{Port: 8080, Service: "http"}},
				Env:   map[string]string{"ROLE": "portal", "SCANNER_URL": "http://scanner:8081"}},
			{Name: "release", Command: []string{"release"},
				Env: map[string]string{"ROLE": "release", "FLAG": "H7CTF{secret}"}},
		},
	})
	if len(pods) != 2 {
		t.Fatalf("multi-container must yield one pod per role, got %d", len(pods))
	}
	byName := map[string]any{}
	for _, p := range pods {
		byName[p.(map[string]any)["name"].(string)] = p
	}
	portal, ok := byName["portal"]
	if !ok {
		t.Fatal("portal pod missing")
	}
	release, ok := byName["release"]
	if !ok {
		t.Fatal("release pod missing")
	}
	// image defaults to the challenge base image
	if got := containersOf(portal)[0].(map[string]any)["image"]; got != "base" {
		t.Fatalf("portal should inherit base image, got %v", got)
	}
	// command -> args (keeps the image ENTRYPOINT)
	if args, _ := containersOf(portal)[0].(map[string]any)["args"].([]any); len(args) != 1 || args[0] != "portal" {
		t.Fatalf("command must map to args, got %v", args)
	}
	// FLAG scoping: only release has it; portal (player-facing) must NOT
	if _, leaked := envOf(containersOf(portal)[0])["FLAG"]; leaked {
		t.Fatal("FLAG leaked into the player-facing portal role")
	}
	if envOf(containersOf(release)[0])["FLAG"] != "H7CTF{secret}" {
		t.Fatal("FLAG must be present in the release role that declared it")
	}
}
