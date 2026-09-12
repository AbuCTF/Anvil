package vm

import (
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
