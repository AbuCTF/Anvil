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

func TestMapPortsHTTP(t *testing.T) {
	s := newTestSvc()
	id := "abcdef0123456789"
	expose, eps := s.mapPorts(id, []PortSpec{{Port: 80, Service: "http"}})
	if len(eps) != 1 || eps[0].Kind != "http" || eps[0].Port != 443 {
		t.Fatalf("bad http endpoint: %+v", eps)
	}
	want := "web-" + id + ".web.h7tex.com"
	if eps[0].Host != want || eps[0].Connect != "https://"+want {
		t.Fatalf("bad host/connect: %+v", eps[0])
	}
	if expose[0]["kind"] != "http" || expose[0]["category"] != "web" {
		t.Fatalf("bad expose: %+v", expose[0])
	}
}

func TestMapPortsTCP(t *testing.T) {
	s := newTestSvc()
	id := "abcdef0123456789"
	_, eps := s.mapPorts(id, []PortSpec{{Port: 1337, Service: "tcp"}})
	want := "pwn-" + id + ".pwn.h7tex.com"
	if eps[0].Kind != "tcp-ssl" || eps[0].Port != 1337 || eps[0].Host != want {
		t.Fatalf("bad tcp endpoint: %+v", eps[0])
	}
	if eps[0].Connect != "ncat --ssl "+want+" 1337" {
		t.Fatalf("bad connect: %q", eps[0].Connect)
	}
}

func TestMapPortsWeb3(t *testing.T) {
	s := newTestSvc()
	id := "abcdef0123456789"
	expose, eps := s.mapPorts(id, []PortSpec{{Port: 1337, Service: "web3"}})
	want := "web3-" + id + ".web3.h7tex.com"
	if eps[0].Kind != "tcp-ssl" || eps[0].Host != want || eps[0].Port != 1337 {
		t.Fatalf("bad web3 endpoint: %+v", eps[0])
	}
	if eps[0].Connect != "ncat --ssl "+want+" 1337" {
		t.Fatalf("bad web3 connect: %q", eps[0].Connect)
	}
	if expose[0]["category"] != "web3" {
		t.Fatalf("web3 expose category should be web3, got %v", expose[0]["category"])
	}
}

func TestMapPortsUniqueHostsSameClass(t *testing.T) {
	s := newTestSvc()
	_, eps := s.mapPorts("id", []PortSpec{{Port: 80, Service: "http"}, {Port: 8080, Service: "http"}})
	if eps[0].Host == eps[1].Host {
		t.Fatalf("two http ports collided on one host: %s", eps[0].Host)
	}
}

func TestExposeKindDefaultsTCP(t *testing.T) {
	// an unset/unknown service is treated as raw TLS, never mis-served as http.
	if exposeKind("") != "tcp-ssl" || exposeKind("weird") != "tcp-ssl" {
		t.Fatal("unknown service must default to tcp-ssl")
	}
	if exposeKind("https") != "https" || routingClass("https", "https") != "web" {
		t.Fatal("https must route as web")
	}
	// web3 is raw TLS like pwn but gets its own subdomain, not "pwn".
	if routingClass("web3", exposeKind("web3")) != "web3" {
		t.Fatal("web3 service must route as web3")
	}
	if routingClass("tcp", "tcp-ssl") != "pwn" {
		t.Fatal("plain tcp must stay pwn")
	}
}
