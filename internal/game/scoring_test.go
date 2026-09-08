package game

import (
	"testing"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

func TestAttackContribution(t *testing.T) {
	cases := []struct {
		captors int
		want    float64
	}{{1, 100}, {2, 50}, {4, 25}, {0, 0}}
	for _, c := range cases {
		if got := attackContribution(100, c.captors); got != c.want {
			t.Errorf("attackContribution(100,%d)=%v want %v", c.captors, got, c.want)
		}
	}
}

func TestDefensePenaltyIsSublinear(t *testing.T) {
	if got := defensePenalty(1, 4); got != 2 {
		t.Errorf("defensePenalty(1,4)=%v want 2", got)
	}
	if got := defensePenalty(1, 9); got != 3 {
		t.Errorf("defensePenalty(1,9)=%v want 3 (sublinear, not 9)", got)
	}
	if got := defensePenalty(1, 0); got != 0 {
		t.Errorf("defensePenalty(1,0)=%v want 0", got)
	}
}

func TestSLATickPoints(t *testing.T) {
	if got := slaTickPoints(10, 25, models.SLAOk); got != 50 { // 10 * sqrt(25)
		t.Errorf("OK: got %v want 50", got)
	}
	if got := slaTickPoints(10, 25, models.SLARecovering); got != 25 {
		t.Errorf("RECOVERING: got %v want 25", got)
	}
	for _, s := range []models.SLAStatus{models.SLADown, models.SLAFaulty, models.SLAFlagNotFound} {
		if got := slaTickPoints(10, 25, s); got != 0 {
			t.Errorf("%s: got %v want 0", s, got)
		}
	}
	if got := slaTickPoints(10, 0, models.SLAOk); got != 10 { // numTeams floored to 1
		t.Errorf("zero teams: got %v want 10", got)
	}
}

func TestKothRankPoints(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	got := kothRankPoints([]int{12, 7, 4, 2, 1}, []teamHold{
		{a, 5}, {b, 2}, {c, 1}, {d, 0},
	})
	if got[a] != 12 || got[b] != 7 || got[c] != 4 {
		t.Errorf("ranks wrong: %v", got)
	}
	if _, ok := got[d]; ok {
		t.Errorf("team with zero hold should score nothing, got %v", got[d])
	}
}

func TestKothRankPointsBeyondTable(t *testing.T) {
	holds := make([]teamHold, 8)
	for i := range holds {
		holds[i] = teamHold{uuid.New(), 8 - i}
	}
	got := kothRankPoints([]int{12, 7, 4, 2, 1}, holds)
	if len(got) != 5 {
		t.Errorf("only top 5 should score, got %d", len(got))
	}
}
