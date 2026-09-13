package vm

import (
	"reflect"
	"strings"
	"testing"
)

func TestGenerateDomainXMLBindsVNCToLoopback(t *testing.T) {
	service := &Service{}

	domainXML, err := service.generateDomainXML(
		"test-vm",
		"4a143477-4a4d-4e96-b2a5-dbc0a70c9080",
		2,
		2048,
		"/var/lib/anvil/images/test.qcow2",
		"52:54:00:12:34:56",
		5901,
		"anvil-vms",
	)
	if err != nil {
		t.Fatalf("generateDomainXML() error = %v", err)
	}
	if strings.Contains(domainXML, "0.0.0.0") {
		t.Fatalf("domain XML exposes VNC on all interfaces: %s", domainXML)
	}
	if !strings.Contains(domainXML, "listen='127.0.0.1'") ||
		!strings.Contains(domainXML, "address='127.0.0.1'") {
		t.Fatalf("domain XML does not bind VNC to loopback: %s", domainXML)
	}
}

func TestOverlayCleanupPathsIncludeConfiguredAndLegacyLocations(t *testing.T) {
	const instanceID = "4a143477-4a4d-4e96-b2a5-dbc0a70c9080"
	want := []string{
		"/srv/anvil/instances/overlays/" + instanceID + ".qcow2",
		legacyOverlayDir + "/" + instanceID + ".qcow2",
	}
	if got := overlayCleanupPaths("/srv/anvil/instances", instanceID); !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay cleanup paths = %#v, want %#v", got, want)
	}

	legacyStore := "/var/lib/anvil/storage/vms"
	if got := overlayCleanupPaths(legacyStore, instanceID); len(got) != 1 {
		t.Fatalf("legacy cleanup paths = %#v, want one deduplicated path", got)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got, want := shellQuote("/tmp/a'b"), `'/tmp/a'"'"'b'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
