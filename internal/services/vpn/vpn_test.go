package vpn

import (
	"net"
	"testing"
)

func TestAbbreviatedPublicKeyHandlesShortAndNormalKeys(t *testing.T) {
	for _, test := range []struct {
		name      string
		publicKey string
		want      string
	}{
		{name: "empty", publicKey: "", want: ""},
		{name: "short", publicKey: "abc", want: "abc"},
		{name: "eight bytes", publicKey: "abcdefgh", want: "abcdefgh"},
		{name: "normal", publicKey: "abcdefghijkl", want: "abcdefgh..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := abbreviatedPublicKey(test.publicKey); got != test.want {
				t.Fatalf("abbreviatedPublicKey(%q) = %q, want %q", test.publicKey, got, test.want)
			}
		})
	}
}

func TestReleaseIPMakesFailedAllocationReusable(t *testing.T) {
	_, ipNetwork, err := net.ParseCIDR("10.10.0.0/29")
	if err != nil {
		t.Fatalf("parse network: %v", err)
	}
	service := &Service{
		usedIPs:   make(map[string]bool),
		ipNetwork: ipNetwork,
		// loadAllocatedIPs stores net.ParseIP's 16-byte representation.
		nextIP: net.ParseIP("10.10.0.2"),
	}

	allocatedIP, err := service.AllocateIP()
	if err != nil {
		t.Fatalf("first AllocateIP() error = %v", err)
	}
	service.ReleaseIP(allocatedIP)

	reallocatedIP, err := service.AllocateIP()
	if err != nil {
		t.Fatalf("second AllocateIP() error = %v", err)
	}
	if reallocatedIP != allocatedIP {
		t.Fatalf("reallocated IP = %q, want released IP %q", reallocatedIP, allocatedIP)
	}
}
