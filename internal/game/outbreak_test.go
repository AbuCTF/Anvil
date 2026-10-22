package game

import (
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// ist builds a wall-clock time in IST for tests.
func ist(year, month, day, hour, min int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, istLocation)
}

func TestCanariedBotReads(t *testing.T) {
	victim := botRead{flagID: uuid.New(), outcome: "crashed"}
	failedCanary := botRead{flagID: uuid.New(), isNOP: true, outcome: "blocked"}
	if got := canariedBotReads([]botRead{victim, failedCanary}); got != nil {
		t.Fatalf("failed NOP canary retained %d bot reads", len(got))
	}

	passedCanary := botRead{flagID: uuid.New(), isNOP: true, outcome: "stolen"}
	if got := canariedBotReads([]botRead{victim, passedCanary}); len(got) != 2 {
		t.Fatalf("successful NOP canary retained %d bot reads, want 2", len(got))
	}
}

func TestReferenceAttackTargetsOnlyGreenSLA(t *testing.T) {
	for _, status := range []models.SLAStatus{models.SLAOk, models.SLARecovering, models.SLAError} {
		if !green(status) {
			t.Errorf("status %s should be eligible", status)
		}
	}
	for _, status := range []models.SLAStatus{models.SLADown, models.SLAFaulty, models.SLAFlagNotFound} {
		if green(status) {
			t.Errorf("status %s should not be eligible", status)
		}
	}
}

func TestInQuietWindow(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"00:00 IST boundary start", ist(2026, 10, 25, 0, 0), true},
		{"00:01 IST inside", ist(2026, 10, 25, 0, 1), true},
		{"03:00 IST middle", ist(2026, 10, 25, 3, 0), true},
		{"05:59 IST last minute", ist(2026, 10, 25, 5, 59), true},
		{"06:00 IST boundary end (open)", ist(2026, 10, 25, 6, 0), false},
		{"06:01 IST just after", ist(2026, 10, 25, 6, 1), false},
		{"15:00 IST afternoon", ist(2026, 10, 24, 15, 0), false},
		{"23:59 IST last tick before quiet", ist(2026, 10, 24, 23, 59), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inQuietWindow(tc.t); got != tc.want {
				t.Errorf("inQuietWindow(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

// TestQuietWaveStart covers the four rulebook cases for wave start time adjustment.
//
//	(1) advisory 23:45 → wave_start 00:15 → adjusted to 06:30
//	(2) first blood 02:00 → advisory deferred to 06:00 → wave_start = 06:30
//	(3) advisory_at 06:00 (S5 fallback) → wave_start = 06:30
//	(4) ongoing wave: advisory 20:00 → wave_start 20:30 → no adjustment (continues through midnight)
func TestQuietWaveStart(t *testing.T) {
	waveDelay := 30 * time.Minute // 15 ticks × 2 min default tick

	cases := []struct {
		name         string
		advisoryTime time.Time
		wantStart    time.Time
	}{
		{
			// case 1: advisory 23:45 IST → wave_start 00:15 IST → pushed to 06:30
			"advisory 23:45 -> wave 06:30",
			ist(2026, 10, 24, 23, 45),
			ist(2026, 10, 25, 6, 30),
		},
		{
			// case 2: advisory deferred to 06:00 IST (first blood was 02:00) → wave 06:30
			"advisory 06:00 (deferred) -> wave 06:30",
			ist(2026, 10, 25, 6, 0),
			ist(2026, 10, 25, 6, 30),
		},
		{
			// case 3: advisory_at 06:00 → wave 06:30
			"advisory_at 06:00 -> wave 06:30",
			ist(2026, 10, 25, 6, 0),
			ist(2026, 10, 25, 6, 30),
		},
		{
			// case 4: ongoing wave from 20:00 → wave_start 20:30 → no quiet adjustment
			"advisory 20:00 -> wave 20:30 (no adjustment)",
			ist(2026, 10, 24, 20, 0),
			ist(2026, 10, 24, 20, 30),
		},
		{
			// boundary: wave start exactly at 06:00 → not in [00:00,06:00) → no push
			"wave_start 06:00 -> unchanged",
			ist(2026, 10, 25, 5, 30), // 05:30 + 30min = 06:00
			ist(2026, 10, 25, 6, 0),
		},
		{
			// deep quiet: advisory 01:00 → wave_start 01:30 → pushed to 06:30
			"advisory 01:00 -> wave 06:30",
			ist(2026, 10, 25, 1, 0),
			ist(2026, 10, 25, 6, 30),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			waveStart := quietWaveStart(tc.advisoryTime.Add(waveDelay))
			if !waveStart.Equal(tc.wantStart) {
				t.Errorf("quietWaveStart(%v + 30min) = %v, want %v",
					tc.advisoryTime, waveStart, tc.wantStart)
			}
		})
	}
}

// TestOngoingWaveContinuesThroughQuiet verifies that a wave whose start time is
// before midnight continues firing during the quiet window (case 4 in the rulebook).
func TestOngoingWaveContinuesThroughQuiet(t *testing.T) {
	waveDelay := 30 * time.Minute
	advisoryTime := ist(2026, 10, 24, 20, 0) // 20:00 IST advisory
	waveStartTime := quietWaveStart(advisoryTime.Add(waveDelay))

	quietTimes := []time.Time{
		ist(2026, 10, 24, 23, 50), // 23:50 IST: wave running before midnight
		ist(2026, 10, 25, 0, 0),   // 00:00 IST: midnight
		ist(2026, 10, 25, 0, 10),  // 00:10 IST: inside quiet window
		ist(2026, 10, 25, 3, 0),   // 03:00 IST: middle of quiet window
		ist(2026, 10, 25, 5, 59),  // 05:59 IST: last minute of quiet window
	}
	for _, tickStart := range quietTimes {
		if tickStart.Before(waveStartTime) {
			t.Errorf("ongoing wave at %v should fire (waveStartTime=%v) but would be skipped",
				tickStart, waveStartTime)
		}
	}
}
