package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/game"
)

func TestArenaTickResponseUsesConfiguredTailMargin(t *testing.T) {
	startedAt := time.Date(2026, time.October, 24, 13, 30, 0, 0, time.UTC)
	handler := &GameHandler{config: &config.Config{Game: config.GameConfig{
		TickInterval: 2 * time.Minute,
		Checker:      config.CheckerConfig{TailMargin: 17 * time.Second},
	}}}

	got, err := json.Marshal(handler.tickResponse(42, startedAt, game.PhaseLive))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tick":42,"started_at":"2026-10-24T13:30:00Z","ends_at":"2026-10-24T13:32:00Z","hold_from":"2026-10-24T13:31:43Z","phase":"live"}`
	if string(got) != want {
		t.Fatalf("tick response = %s, want %s", got, want)
	}
}

func TestMeStatusServiceJSONContract(t *testing.T) {
	port := 9001
	fate := "held"
	deployedAt := time.Date(2026, time.October, 24, 13, 45, 0, 0, time.UTC)
	got, err := json.Marshal(meServiceRow{
		ServiceID: "service-id",
		Slug:      "service",
		Name:      "Service",
		Port:      &port,
		Stores: []meStoreRow{{
			StoreIndex: 0,
			StoreName:  "flag 1",
			Steal:      1,
			Hold:       2,
			Up:         3,
			Ticks:      []meTickRow{{Tick: 4, Status: "OK", FlagFate: &fate}},
		}},
		PointsThisHour:  mePointsHour{Steal: 1, Hold: 2, Up: 3},
		ActiveAttackers: 1,
		LastDeployment:  &meDeployment{Kind: "deploy", Tick: new(7), DeployedAt: deployedAt},
		DeploymentHistory: []meDeploymentEvent{{
			State: "gating", Message: "Running Deployment Gate.", RecordedAt: deployedAt,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"service_id":"service-id","slug":"service","name":"Service","port":9001,"stores":[{"store_index":0,"store_name":"flag 1","steal":1,"hold":2,"up":3,"ticks":[{"tick":4,"status":"OK","flag_fate":"held"}]}],"points_this_hour":{"steal":1,"hold":2,"up":3},"active_attackers":1,"last_deployment":{"kind":"deploy","org_initiated":false,"tick":7,"deployed_at":"2026-10-24T13:45:00Z"},"deployment_history":[{"state":"gating","message":"Running Deployment Gate.","recorded_at":"2026-10-24T13:45:00Z"}]}`
	if string(got) != want {
		t.Fatalf("me status JSON = %s", got)
	}
}

func TestDeploymentEventMessagesAreAllowListed(t *testing.T) {
	tests := map[string]string{
		"queued":               "Change queued.",
		"waiting_quiet_window": "Gate passed; waiting for a safe promotion window.",
		"promoted":             "Deployment promoted and under health observation.",
		"failed/gate":          "Deployment Gate rejected the candidate.",
		"failed/operator":      "Deployment paused for organizer recovery.",
	}
	for input, want := range tests {
		state, reason := input, ""
		if input == "failed/gate" {
			state, reason = "failed", "gate"
		}
		if input == "failed/operator" {
			state, reason = "failed", "operator"
		}
		if got := deploymentEventMessage(state, reason); got != want {
			t.Errorf("deploymentEventMessage(%q, %q) = %q, want %q", state, reason, got, want)
		}
	}
}
