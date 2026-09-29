package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
)

func TestSwarmRuntimeIDRoundTrip(t *testing.T) {
	encoded := encodeSwarmRuntimeID("service123", "anvil-i-0123456789ab")
	got, err := parseSwarmRuntimeID(encoded)
	if err != nil {
		t.Fatalf("parseSwarmRuntimeID() error = %v", err)
	}
	if got.serviceID != "service123" || got.networkName != "anvil-i-0123456789ab" {
		t.Fatalf("parseSwarmRuntimeID() = %#v", got)
	}
	for _, value := range []string{
		"container123",
		"swarm:service123",
		"swarm:../service:network",
		"swarm:service:../network",
	} {
		if _, err := parseSwarmRuntimeID(value); err == nil {
			t.Fatalf("parseSwarmRuntimeID(%q) error = nil", value)
		}
	}
}

func TestNormalizeSwarmArchitecture(t *testing.T) {
	tests := map[string]string{
		"":               "amd64",
		"linux/amd64":    "amd64",
		"linux/arm64/v8": "arm64",
		"x86_64":         "amd64",
		"aarch64":        "arm64",
	}
	for input, want := range tests {
		got, err := normalizeSwarmArchitecture(input, "amd64")
		if err != nil || got != want {
			t.Fatalf("normalizeSwarmArchitecture(%q) = (%q, %v), want (%q, nil)", input, got, err, want)
		}
	}
	if _, err := normalizeSwarmArchitecture("linux/riscv64", "amd64"); err == nil {
		t.Fatal("normalizeSwarmArchitecture(riscv64) error = nil")
	}
}

func TestSwarmHTTPRouteDocument(t *testing.T) {
	document, err := swarmHTTPRouteDocument(
		"anvil-i-0123456789ab",
		"0123456789abcdef.demo.h7tex.com",
		"http",
		31000,
	)
	if err != nil {
		t.Fatalf("swarmHTTPRouteDocument() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("route document is not valid JSON: %v", err)
	}
	if !json.Valid(document) {
		t.Fatal("route document is invalid JSON")
	}
	text := string(document)
	for _, expected := range []string{
		"Host(`0123456789abcdef.demo.h7tex.com`)",
		"http://127.0.0.1:31000",
		"anvil-i-0123456789ab",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("route document does not contain %q: %s", expected, text)
		}
	}
	if _, err := swarmHTTPRouteDocument("router", "bad host", "http", 31000); err == nil {
		t.Fatal("swarmHTTPRouteDocument() accepted unsafe hostname")
	}
}

func TestWriteFileAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes", "instance.json")
	if err := writeFileAtomically(path, []byte("first"), 0o640); err != nil {
		t.Fatalf("writeFileAtomically(first) error = %v", err)
	}
	if err := writeFileAtomically(path, []byte("second"), 0o640); err != nil {
		t.Fatalf("writeFileAtomically(second) error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("route contents = %q, want second", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("route mode = %o, want 640", got)
	}
}

func TestChallengeNetworkCreateOptionsDisablesInterContainerCommunication(t *testing.T) {
	cfg := config.ContainerConfig{
		NetworkSubnet:   "172.20.0.0/16",
		NetworkInternal: true,
		Labels: map[string]string{
			"managed-by": "anvil",
		},
	}

	options, err := challengeNetworkCreateOptions(cfg)
	if err != nil {
		t.Fatalf("challengeNetworkCreateOptions() error = %v", err)
	}

	if options.Driver != "bridge" {
		t.Fatalf("Driver = %q, want bridge", options.Driver)
	}
	if got := options.Options[interContainerCommunicationOption]; got != "false" {
		t.Fatalf("%s = %q, want false", interContainerCommunicationOption, got)
	}
	if !options.Internal {
		t.Fatal("Internal = false, want true")
	}
	if options.IPAM == nil || len(options.IPAM.Config) != 1 || options.IPAM.Config[0].Subnet.String() != cfg.NetworkSubnet {
		t.Fatalf("IPAM config = %#v, want subnet %q", options.IPAM, cfg.NetworkSubnet)
	}
	if options.Labels["managed-by"] != "anvil" {
		t.Fatalf("Labels = %#v, want configured labels", options.Labels)
	}
}

func TestHTTPRoutingLabels(t *testing.T) {
	labels, err := httpRoutingLabels(
		"anvil-afterglow-01234567",
		"0123456789abcdef.demo.h7tex.com",
		[]ExposedPort{{Port: 8080, Protocol: "http", Service: "http"}},
	)
	if err != nil {
		t.Fatalf("httpRoutingLabels() error = %v", err)
	}
	if got := labels["traefik.enable"]; got != "true" {
		t.Fatalf("traefik.enable = %q, want true", got)
	}
	if got := labels["traefik.http.routers.anvil-afterglow-01234567.rule"]; got != "Host(`0123456789abcdef.demo.h7tex.com`)" {
		t.Fatalf("router rule = %q", got)
	}
	if got := labels["traefik.http.services.anvil-afterglow-01234567.loadbalancer.server.port"]; got != "8080" {
		t.Fatalf("service port = %q, want 8080", got)
	}
}

func TestHTTPRoutingLabelsRejectsUnsafeHost(t *testing.T) {
	_, err := httpRoutingLabels(
		"router",
		"demo.h7tex.com`) || Host(`attacker.example",
		[]ExposedPort{{Port: 8080, Service: "http"}},
	)
	if err == nil {
		t.Fatal("httpRoutingLabels() error = nil, want invalid hostname error")
	}
}

func TestHTTPRoutingLabelsRequiresHTTPPort(t *testing.T) {
	_, err := httpRoutingLabels(
		"router",
		"0123456789abcdef.demo.h7tex.com",
		[]ExposedPort{{Port: 1337, Protocol: "tcp", Service: "tcp"}},
	)
	if err == nil {
		t.Fatal("httpRoutingLabels() error = nil, want missing HTTP port error")
	}
}

func TestTransportProtocolNormalizesApplicationProtocols(t *testing.T) {
	for _, protocol := range []string{"", "tcp", "http", "https"} {
		if got := transportProtocol(protocol); got != "tcp" {
			t.Fatalf("transportProtocol(%q) = %q, want tcp", protocol, got)
		}
	}
	if got := transportProtocol("udp"); got != "udp" {
		t.Fatalf("transportProtocol(udp) = %q, want udp", got)
	}
}

func TestRawTCPPortClassification(t *testing.T) {
	tests := []struct {
		name string
		port ExposedPort
		want bool
	}{
		{name: "tcp", port: ExposedPort{Port: 1337, Protocol: "tcp", Service: "tcp"}, want: true},
		{name: "implicit tcp", port: ExposedPort{Port: 1337}, want: true},
		{name: "http service", port: ExposedPort{Port: 8080, Protocol: "tcp", Service: "http"}, want: false},
		{name: "http protocol", port: ExposedPort{Port: 8080, Protocol: "http"}, want: false},
		{name: "udp", port: ExposedPort{Port: 5353, Protocol: "udp", Service: "dns"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRawTCPPort(tt.port); got != tt.want {
				t.Fatalf("isRawTCPPort(%#v) = %t, want %t", tt.port, got, tt.want)
			}
		})
	}
}

func TestNextAvailableTCPPort(t *testing.T) {
	used := map[int]struct{}{30000: {}}
	available := func(port int) bool { return port != 30001 }

	got, err := nextAvailableTCPPort(30000, 30003, used, available)
	if err != nil {
		t.Fatalf("nextAvailableTCPPort() error = %v", err)
	}
	if got != 30002 {
		t.Fatalf("nextAvailableTCPPort() = %d, want 30002", got)
	}
	if _, ok := used[30002]; !ok {
		t.Fatal("nextAvailableTCPPort() did not reserve selected port")
	}
}

func TestNextAvailableTCPPortExhausted(t *testing.T) {
	used := map[int]struct{}{30000: {}, 30001: {}}
	if _, err := nextAvailableTCPPort(30000, 30001, used, nil); err == nil {
		t.Fatal("nextAvailableTCPPort() error = nil, want exhausted error")
	}
}

func TestParseResourceLimits(t *testing.T) {
	cpuTests := map[string]int64{
		"0.5":  500_000_000,
		"500m": 500_000_000,
		"2":    2_000_000_000,
	}
	for input, want := range cpuTests {
		got, err := parseCPULimit(input)
		if err != nil || got != want {
			t.Fatalf("parseCPULimit(%q) = (%d, %v), want (%d, nil)", input, got, err, want)
		}
	}

	memoryTests := map[string]int64{
		"256Mi": 256 * 1024 * 1024,
		"512M":  512 * 1024 * 1024,
		"1Gi":   1024 * 1024 * 1024,
	}
	for input, want := range memoryTests {
		got, err := parseMemoryLimit(input)
		if err != nil || got != want {
			t.Fatalf("parseMemoryLimit(%q) = (%d, %v), want (%d, nil)", input, got, err, want)
		}
	}

	for _, input := range []string{"-1", "0", "garbage"} {
		if _, err := parseCPULimit(input); err == nil {
			t.Fatalf("parseCPULimit(%q) error = nil", input)
		}
		if _, err := parseMemoryLimit(input); err == nil {
			t.Fatalf("parseMemoryLimit(%q) error = nil", input)
		}
	}
}

func TestChallengeNetworkCreateOptionsRejectsInvalidSubnet(t *testing.T) {
	_, err := challengeNetworkCreateOptions(config.ContainerConfig{NetworkSubnet: "not-a-subnet"})
	if err == nil {
		t.Fatal("challengeNetworkCreateOptions() error = nil, want invalid subnet error")
	}
}
