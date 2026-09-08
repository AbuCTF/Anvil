package game

import (
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
)

func TestRoundForTick(t *testing.T) {
	c := &Controller{cfg: config.GameConfig{
		TickInterval: time.Minute,
		Koth:         config.KothConfig{RoundInterval: 5 * time.Minute},
	}}
	if got := c.ticksPerRound(); got != 5 {
		t.Fatalf("ticksPerRound: got %d want 5", got)
	}
	for tick, want := range map[int]int{1: 1, 5: 1, 6: 2, 10: 2, 11: 3} {
		if got := c.roundForTick(tick); got != want {
			t.Errorf("roundForTick(%d): got %d want %d", tick, got, want)
		}
	}
}

func TestTicksPerRoundFloor(t *testing.T) {
	c := &Controller{cfg: config.GameConfig{TickInterval: time.Minute}}
	if got := c.ticksPerRound(); got != 1 {
		t.Fatalf("ticksPerRound floor: got %d want 1", got)
	}
}
