package handlers

import (
	"testing"
	"time"
)

func TestPulseSolveBandProtectsSmallSamples(t *testing.T) {
	for _, captures := range []int{0, 1, 2} {
		if got := pulseSolveBand(captures); got != "insufficient_sample" {
			t.Fatalf("captures=%d band=%q, want insufficient_sample", captures, got)
		}
	}
	tests := []struct {
		captures int
		want     string
	}{
		{3, "few"}, {5, "few"}, {6, "several"}, {10, "several"},
		{11, "crowded"}, {25, "crowded"}, {26, "saturated"},
	}
	for _, tt := range tests {
		if got := pulseSolveBand(tt.captures); got != tt.want {
			t.Errorf("captures=%d band=%q, want %q", tt.captures, got, tt.want)
		}
	}
}

func TestPulseHeatAndDirectionProtectSmallMovements(t *testing.T) {
	for _, recent := range []int{0, 1, 2} {
		if got := pulseHeat(recent); got != "quiet" {
			t.Errorf("recent=%d heat=%q, want quiet", recent, got)
		}
	}
	if got := pulseHeat(3); got != "warming" {
		t.Errorf("heat(3)=%q, want warming", got)
	}
	if got := pulseHeat(6); got != "active" {
		t.Errorf("heat(6)=%q, want active", got)
	}
	if got := pulseHeat(11); got != "hot" {
		t.Errorf("heat(11)=%q, want hot", got)
	}

	tests := []struct {
		recent, previous int
		want             string
	}{
		{0, 0, "insufficient_sample"},
		{1, 2, "insufficient_sample"},
		{3, 0, "accelerating"},
		{6, 3, "accelerating"},
		{3, 6, "cooling"},
		{4, 4, "steady"},
	}
	for _, tt := range tests {
		if got := pulseDirection(tt.recent, tt.previous); got != tt.want {
			t.Errorf("direction(%d,%d)=%q, want %q", tt.recent, tt.previous, got, tt.want)
		}
	}
}

func TestNextP2CRateDecaysAndFloors(t *testing.T) {
	tests := []struct {
		blocks int
		want   float64
	}{
		{-1, 1}, {0, 1}, {1, 0.7}, {2, 0.49}, {20, 0.05},
	}
	for _, tt := range tests {
		if got := nextP2CRate(tt.blocks, 1, 0.7, 0.05); got < tt.want-1e-9 || got > tt.want+1e-9 {
			t.Errorf("nextP2CRate(%d)=%v, want %v", tt.blocks, got, tt.want)
		}
	}
}

func TestMarketPulseNoticesUseOnlyTeamState(t *testing.T) {
	now := time.Now()
	h := &EconomyHandler{}
	response := h.marketPulseNotices(pulseTeam{
		Credits: 40, OpenSlotsUsed: 3, OpenSlotsTotal: 3,
		Affordability: []pulseAffordability{{Difficulty: "easy", Cost: 50}},
		Open:          []pulseOpen{{Slug: "x", Name: "Expiring", ExpiresAt: timePointer(now.Add(8 * time.Minute))}},
	}, true, now)
	if len(response) != 4 {
		t.Fatalf("notices=%v, want freeze + slots + runway + timer", response)
	}
	for _, notice := range response {
		if notice.Message == "" || notice.Kind == "" || notice.Severity == "" {
			t.Fatalf("incomplete notice: %+v", notice)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }
