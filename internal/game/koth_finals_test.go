package game

import (
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/google/uuid"
)

func TestRoundLengthDayAndNight(t *testing.T) {
	k := config.KothConfig{RoundInterval: 15 * time.Minute, NightRoundInterval: 30 * time.Minute,
		NightStart: "00:00", NightEnd: "07:00", Timezone: "Asia/Kolkata"}
	ist, _ := time.LoadLocation("Asia/Kolkata")
	for _, tt := range []struct {
		at   time.Time
		want time.Duration
	}{
		{time.Date(2026, 10, 24, 23, 59, 0, 0, ist), 15 * time.Minute},
		{time.Date(2026, 10, 25, 0, 0, 0, 0, ist), 30 * time.Minute},
		{time.Date(2026, 10, 25, 6, 59, 0, 0, ist), 30 * time.Minute},
		{time.Date(2026, 10, 25, 7, 0, 0, 0, ist), 15 * time.Minute},
		{time.Date(2026, 10, 24, 19, 0, 0, 0, time.UTC), 30 * time.Minute}, // 00:30 IST
	} {
		if got := roundLength(k, tt.at); got != tt.want {
			t.Errorf("round at %s = %s, want %s", tt.at, got, tt.want)
		}
	}
	k.NightRoundInterval = 0
	if got := roundLength(k, time.Date(2026, 10, 25, 1, 0, 0, 0, ist)); got != 15*time.Minute {
		t.Errorf("no night interval should keep the day length, got %s", got)
	}
	k.NightStart, k.NightEnd, k.NightRoundInterval = "22:00", "06:00", time.Hour
	if !atNight(k, time.Date(2026, 10, 24, 23, 0, 0, 0, ist)) || atNight(k, time.Date(2026, 10, 24, 21, 0, 0, 0, ist)) {
		t.Error("a night window across midnight is wrong")
	}
}

func TestRankPointsSplitShareTies(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pts := rankPointsSplit([]int{12, 7, 4, 2, 1}, []teamHold{{a, 5}, {b, 5}, {c, 3}, {d, 0}})
	if pts[a] != 9.5 || pts[b] != 9.5 || pts[c] != 4 || pts[d] != 0 {
		t.Fatalf("tie split = %v, want a,b 9.5 c 4 d 0", pts)
	}
	pts = rankPointsSplit([]int{12, 7}, []teamHold{{a, 2}, {b, 2}, {c, 2}})
	if !near(pts[a], 19.0/3) || !near(pts[c], 19.0/3) {
		t.Fatalf("a three-way tie beyond the table must share what exists: %v", pts)
	}
}

func TestSoleLeaderAndCrownBounty(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if soleLeader([]teamHold{{a, 4}, {b, 4}}) != nil {
		t.Fatal("a tie at the top crowned someone")
	}
	if w := soleLeader([]teamHold{{a, 4}, {b, 6}}); w == nil || *w != b {
		t.Fatal("sole leader wrong")
	}
	k := config.KothConfig{BountyPerRound: 2, BountyMinStreak: 3, BountyCap: 10}
	if got := crownBounty(k, &a, 4, &b); got != 8 {
		t.Fatalf("bounty for ending a 4-round reign = %v, want 8", got)
	}
	if got := crownBounty(k, &a, 7, &b); got != 10 {
		t.Fatalf("bounty cap = %v, want 10", got)
	}
	if crownBounty(k, &a, 2, &b) != 0 || crownBounty(k, &a, 5, &a) != 0 || crownBounty(k, nil, 5, &b) != 0 {
		t.Fatal("bounty paid where it should not be")
	}
	k.BountyPerRound = 0
	if crownBounty(k, &a, 9, &b) != 0 {
		t.Fatal("bounty paid while off")
	}
}
