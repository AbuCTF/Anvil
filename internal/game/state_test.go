package game

import (
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/models"
)

func TestParseState(t *testing.T) {
	for raw, want := range map[string]State{
		"":            StateLegacy,
		"running":     StateRunning,
		" Running ":   StateRunning,
		"paused":      StatePaused,
		"stopped":     StateStopped,
		"runing":      StatePaused, // a typo stops the clock, never runs it
		"\"running\"": StatePaused,
	} {
		if got := ParseState(raw); got != want {
			t.Errorf("ParseState(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPhaseAt(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	var unset time.Time
	for _, tt := range []struct {
		name       string
		state      State
		start, end time.Time
		want       Phase
	}{
		{"running, no window", StateRunning, unset, unset, PhaseLive},
		{"running, inside window", StateRunning, past, future, PhaseLive},
		{"running, before start", StateRunning, future, unset, PhaseNotStarted},
		{"running, after end", StateRunning, past, past, PhaseOver},
		{"running, end exactly now", StateRunning, past, now, PhaseOver},
		{"paused inside window", StatePaused, past, future, PhasePaused},
		{"paused after end", StatePaused, past, past, PhaseOver},
		{"stopped", StateStopped, past, future, PhaseOver},
	} {
		if got := phaseAt(tt.state, now, tt.start, tt.end); got != tt.want {
			t.Errorf("%s: phase = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestAggregate(t *testing.T) {
	ok := Result{Status: models.SLAOk}
	for _, tt := range []struct {
		name         string
		current, old []Result
		want         models.SLAStatus
	}{
		{"all ok", []Result{ok, ok}, []Result{ok}, models.SLAOk},
		{"worst current wins", []Result{ok, {Status: models.SLAFlagNotFound}, {Status: models.SLADown}}, nil, models.SLADown},
		{"organizer fault outranks down", []Result{{Status: models.SLADown}, {Status: models.SLAError}}, nil, models.SLAError},
		{"lost old flag recovers", []Result{ok}, []Result{ok, {Status: models.SLAFlagNotFound}}, models.SLARecovering},
		{"botched old check is not the team's", []Result{ok}, []Result{{Status: models.SLAError}}, models.SLAOk},
		{"current failure beats recovering", []Result{{Status: models.SLAFaulty}}, []Result{{Status: models.SLAFlagNotFound}}, models.SLAFaulty},
	} {
		got, ok := aggregate(tt.current, tt.old)
		if !ok || got.Status != tt.want {
			t.Errorf("%s: verdict = %s (ok=%v), want %s", tt.name, got.Status, ok, tt.want)
		}
	}
	if _, ok := aggregate(nil, nil); ok {
		t.Error("no tasks should produce no verdict")
	}
}

func TestTickPlanWindows(t *testing.T) {
	d := &Dispatcher{tickLength: 120 * time.Second, checker: config.CheckerConfig{
		TaskTimeout: 30 * time.Second, TailMargin: 10 * time.Second, Spread: true}}
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	p := d.plan(start)
	if p.timeout != 30*time.Second || !p.end.Equal(start.Add(110*time.Second)) {
		t.Fatalf("plan timeout=%s end=%s", p.timeout, p.end)
	}
	latest := p.end.Add(-p.timeout) // 80 s in
	for i := 0; i < 500; i++ {
		put := p.pick(0, 1)
		if put.Before(start) || !put.Before(start.Add(30*time.Second)) {
			t.Fatalf("putflag at %s outside the first quarter", put.Sub(start))
		}
		cur := p.pick(2, 3)
		if cur.Before(start.Add(60*time.Second)) || cur.After(latest) {
			t.Fatalf("current getflag at %s outside [60s, 80s]", cur.Sub(start))
		}
		// a put that runs its full timeout still finishes before the current get starts
		if put.Add(p.timeout).After(start.Add(60 * time.Second)) {
			t.Fatalf("putflag at %s can overlap the current getflag window", put.Sub(start))
		}
		if h := p.pick(0, 3); h.After(latest) {
			t.Fatalf("havoc at %s finishes inside the tail margin", h.Sub(start))
		}
	}

	// a task timeout longer than a quarter is clamped, so put still precedes get
	d.checker.TaskTimeout = time.Minute
	if p := d.plan(start); p.timeout != 30*time.Second {
		t.Fatalf("timeout not clamped to a quarter: %s", p.timeout)
	}
	d.checker.Spread = false
	if got := d.plan(start).pick(2, 3); !got.Equal(start) {
		t.Fatalf("unspread task should start immediately, got +%s", got.Sub(start))
	}
}
