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
