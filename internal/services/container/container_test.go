package container

import (
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
)

func TestChallengeNetworkCreateOptionsDisablesInterContainerCommunication(t *testing.T) {
	cfg := config.ContainerConfig{
		NetworkSubnet: "172.20.0.0/16",
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
	if options.IPAM == nil || len(options.IPAM.Config) != 1 || options.IPAM.Config[0].Subnet.String() != cfg.NetworkSubnet {
		t.Fatalf("IPAM config = %#v, want subnet %q", options.IPAM, cfg.NetworkSubnet)
	}
	if options.Labels["managed-by"] != "anvil" {
		t.Fatalf("Labels = %#v, want configured labels", options.Labels)
	}
}

func TestChallengeNetworkCreateOptionsRejectsInvalidSubnet(t *testing.T) {
	_, err := challengeNetworkCreateOptions(config.ContainerConfig{NetworkSubnet: "not-a-subnet"})
	if err == nil {
		t.Fatal("challengeNetworkCreateOptions() error = nil, want invalid subnet error")
	}
}
