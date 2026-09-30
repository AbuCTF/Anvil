package handlers

import (
	"strings"
	"testing"
)

func validChallengeRequest() CreateChallengeRequest {
	return CreateChallengeRequest{
		Name:        "Challenge",
		Difficulty:  "medium",
		BasePoints:  100,
		ScoringMode: "flag",
		ArenaMode:   "per_team",
		Flags:       []FlagInput{{Name: "Flag", Flag: "H7CTF{test}", Points: 100, FlagType: "static"}},
	}
}

func TestValidateChallengeRequestAcceptsSupportedDeliveryModels(t *testing.T) {
	for _, mutate := range []func(*CreateChallengeRequest){
		func(request *CreateChallengeRequest) {},
		func(request *CreateChallengeRequest) {
			request.ContainerImage = "ghcr.io/example/app"
			request.ExposedPorts = append(request.ExposedPorts, struct {
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
				Service  string `json:"service"`
			}{Port: 8080, Protocol: "tcp", Service: "http"})
		},
		func(request *CreateChallengeRequest) {
			request.Services = []ContainerService{{Name: "app", Image: "ghcr.io/example/app", Public: true}}
		},
	} {
		request := validChallengeRequest()
		mutate(&request)
		if err := validateChallengeRequest(&request, true); err != nil {
			t.Fatalf("supported challenge rejected: %v", err)
		}
	}
}

func TestValidateChallengeRequestRejectsInvalidRuntimeAndFlags(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*CreateChallengeRequest)
		contains string
	}{
		{name: "duplicate port", contains: "duplicated", mutate: func(request *CreateChallengeRequest) {
			request.ExposedPorts = append(request.ExposedPorts,
				struct {
					Port     int    `json:"port"`
					Protocol string `json:"protocol"`
					Service  string `json:"service"`
				}{Port: 1337, Protocol: "tcp", Service: "tcp"},
				struct {
					Port     int    `json:"port"`
					Protocol string `json:"protocol"`
					Service  string `json:"service"`
				}{Port: 1337, Protocol: "tcp", Service: "tcp"})
		}},
		{name: "unsafe service name", contains: "DNS-safe", mutate: func(request *CreateChallengeRequest) {
			request.Services = []ContainerService{{Name: "DB_PRIMARY", Image: "postgres"}}
		}},
		{name: "missing image", contains: "needs an image", mutate: func(request *CreateChallengeRequest) {
			request.Services = []ContainerService{{Name: "app"}}
		}},
		{name: "invalid image", contains: "valid registry image reference", mutate: func(request *CreateChallengeRequest) {
			request.ContainerImage = "https://docker.io/example/app"
		}},
		{name: "invalid service image", contains: "valid registry image reference", mutate: func(request *CreateChallengeRequest) {
			request.Services = []ContainerService{{Name: "app", Image: "example app"}}
		}},
		{name: "invalid regex", contains: "invalid regular expression", mutate: func(request *CreateChallengeRequest) {
			request.Flags = []FlagInput{{Name: "Flag", Flag: "[", FlagType: "regex"}}
		}},
		{name: "missing dynamic prefix", contains: "dynamic prefix", mutate: func(request *CreateChallengeRequest) {
			request.Flags = []FlagInput{{Name: "Flag", FlagType: "dynamic"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := validChallengeRequest()
			test.mutate(&request)
			err := validateChallengeRequest(&request, true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
		})
	}
}
