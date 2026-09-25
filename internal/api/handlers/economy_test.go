package handlers

import (
	"math"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
)

// shippingEconomy mirrors the config defaults (×10 player anchor, shipping per-band
// crowd floors) so the tests pin the exact numbers in economy_test_vectors.md.
func shippingEconomy() config.EconomyConfig {
	return config.EconomyConfig{
		Grant:           4000,
		Ceilings:        []float64{100, 250, 500, 1000},
		LaunchCosts:     []float64{50, 100, 200, 250},
		CrowdFloors:     []float64{0.15, 0.15, 0.15, 0.75},
		CrowdHalflives:  []float64{8, 12, 20, 40},
		WrongSubPenalty: 0.25,
		WrongSubFloor:   0.2,
		C2PRate:         0.015,
		FreeFlagPoints:  10,
	}
}

func approx(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s: got %.4f, want %.4f", label, got, want)
	}
}

// TestChallengeValue pins the crowd-decay + wrong-sub math to economy_test_vectors.md §3, §4.
func TestChallengeValue(t *testing.T) {
	cfg := shippingEconomy()
	// §4 crowd decay (0 wrong subs)
	approx(t, "easy s=0", challengeValue(cfg, "easy", 0, 0), 100.0)
	approx(t, "easy s=8 (halflife)", challengeValue(cfg, "easy", 8, 0), 57.5)
	approx(t, "easy s=16 (2*halflife)", challengeValue(cfg, "easy", 16, 0), 36.25)
	approx(t, "medium s=12 (halflife)", challengeValue(cfg, "medium", 12, 0), 143.75)
	approx(t, "hard s=20 (halflife)", challengeValue(cfg, "hard", 20, 0), 287.5)
	approx(t, "novel s=0", challengeValue(cfg, "insane", 0, 0), 1000.0)
	approx(t, "novel s=40 (halflife)", challengeValue(cfg, "insane", 40, 0), 875.0)
	// §3 wrong-sub penalty (undecayed hard, 2 wrong)
	approx(t, "hard s=0 wrong=2", challengeValue(cfg, "hard", 0, 2), 281.25)
	// wrong-sub floor holds at high counts
	approx(t, "easy s=0 wrong=8 (floor)", challengeValue(cfg, "easy", 0, 8), 100.0*0.2)
}

// TestIntegrationScore reproduces economy_test_vectors.md §10 end-to-end total.
func TestIntegrationScore(t *testing.T) {
	cfg := shippingEconomy()
	easy := challengeValue(cfg, "easy", 16, 0)     // 36.25
	medium := challengeValue(cfg, "medium", 12, 0) // 143.75
	hard := challengeValue(cfg, "hard", 0, 2)      // 281.25
	realised := easy + medium + hard
	approx(t, "realised", realised, 461.25)
	freeze := 300.0 * cfg.C2PRate // 4.5
	final := realised + freeze + cfg.FreeFlagPoints
	approx(t, "final score", final, 475.75)
}

func TestFractionalValue(t *testing.T) {
	cfg := config.EconomyConfig{
		Ceilings:       []float64{100, 250, 500, 1000},
		CrowdFloors:    []float64{0.15, 0.15, 0.15, 0.15},
		CrowdHalflives: []float64{20, 20, 20, 20},
		WrongSubFloor:  1,
	}
	// 4 equal flags: one team holds all, one holds a quarter -> crowd 1.25
	crowd := 1.0 + shareOf(100, 400, 1, 4)
	full := challengeValue(cfg, "hard", crowd, 0) * 1.0
	quarter := challengeValue(cfg, "hard", crowd, 0) * shareOf(100, 400, 1, 4)
	if math.Abs(full-481.98) > 0.01 || math.Abs(quarter-120.50) > 0.01 {
		t.Fatalf("got %.2f / %.2f, want 481.98 / 120.50", full, quarter)
	}
	// single-flag parity: three full solvers price exactly like the old integer count
	if got, want := challengeValue(cfg, "hard", 3, 0), 500*(0.15+0.85*math.Pow(0.5, 3.0/20)); math.Abs(got-want) > 1e-9 {
		t.Fatalf("single-flag parity: got %v want %v", got, want)
	}
}

func TestShareOf(t *testing.T) {
	cases := []struct {
		held, total   float64
		heldN, totalN int
		want          float64
	}{
		{200, 800, 1, 4, 0.25}, // weighted by points
		{0, 0, 1, 2, 0.5},      // all-zero points: equal weights
		{800, 800, 4, 4, 1},
		{0, 0, 0, 0, 1}, // flagless (graded): full share
	}
	for _, c := range cases {
		if got := shareOf(c.held, c.total, c.heldN, c.totalN); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("shareOf(%v,%v,%v,%v) = %v, want %v", c.held, c.total, c.heldN, c.totalN, got, c.want)
		}
	}
}
