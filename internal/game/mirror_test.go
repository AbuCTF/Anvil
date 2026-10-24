package game

import (
	"math"
	"testing"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// a one-store world: 15 teams + a nop, flags live one tick (planted at tick 1,
// valid through tick 1), scored while closing tick 2 so everything is settled
type world struct {
	in      *mirrorInput
	teams   []uuid.UUID
	nop     uuid.UUID
	service uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{nop: uuid.New(), service: uuid.New()}
	w.in = &mirrorInput{
		N: 15, Current: 2,
		Teams:      map[uuid.UUID]bool{w.nop: true},
		Stores:     map[uuid.UUID]int{w.service: 1},
		SLA:        map[slaKey]slaTick{},
		RecShare:   map[slaKey]float64{},
		BotCrashed: map[slaKey]bool{},
		NopStolen:  map[storeTick]bool{},
		Attackers:  map[storeTick]map[uuid.UUID]bool{},
		Steal:      map[teamStore]float64{},
		Koth:       map[uuid.UUID]float64{},
		MassFault:  3, WeightAD: 0.7, WeightKoth: 0.3,
	}
	for i := 0; i < 15; i++ {
		id := uuid.New()
		w.teams = append(w.teams, id)
		w.in.Teams[id] = false
	}
	return w
}

func (w *world) st() storeTick { return storeTick{w.service, 0, 1} }

// flag adds team's tick-1 flag; up sets its tick-1 sla to OK
func (w *world) flag(team uuid.UUID, captors int, up bool) {
	var unread []int
	if up {
		w.in.SLA[slaKey{team, w.service, 1}] = slaTick{Status: models.SLAOk, Step: "getflag"}
	} else {
		unread = []int{1}
		w.in.SLA[slaKey{team, w.service, 1}] = slaTick{Status: models.SLADown, Step: "putflag"}
	}
	w.in.Flags = append(w.in.Flags, mirrorFlag{ID: uuid.New(), Team: team, Service: w.service, Tick: 1, ValidUntil: 1,
		Unread: unread, Captors: captors})
}

func (w *world) attackers(ids ...uuid.UUID) {
	m := map[uuid.UUID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	w.in.Attackers[w.st()] = m
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestMirrorWorkedExample(t *testing.T) {
	up := math.Sqrt(15)

	// 06 section 3.4: six teams exploit the store; the only team that patched holds
	w := newWorld(t)
	w.attackers(w.teams[0:6]...)
	holder := w.teams[14]
	w.flag(holder, 0, true)
	for _, v := range w.teams[6:14] {
		w.flag(v, 6, true) // unpatched, stolen by all six
	}
	r := scoreMirror(w.in)
	if s := r.Teams[holder]; !near(s.Hold, 12) || !near(s.Up, up) {
		t.Fatalf("lone holder: hold %.3f up %.3f, want 12 and %.3f", s.Hold, s.Up, up)
	}
	if s := r.Teams[w.teams[6]]; s.Hold != 0 || !near(s.Up, up) {
		t.Fatalf("exploited but up: hold %.3f up %.3f, want 0 and %.3f", s.Hold, s.Up, up)
	}

	// eight teams held: each gets 6 x (1 + 1/8)
	w = newWorld(t)
	w.attackers(w.teams[0:6]...)
	for _, v := range w.teams[6:14] {
		w.flag(v, 0, true)
	}
	r = scoreMirror(w.in)
	if s := r.Teams[w.teams[6]]; !near(s.Hold, 6.75) {
		t.Fatalf("one of eight holders: hold %.3f, want 6.75", s.Hold)
	}
	// an attacker that also held: A excludes itself
	w.flag(w.teams[0], 0, true)
	r = scoreMirror(w.in)
	if s := r.Teams[w.teams[0]]; !near(s.Hold, 5*(1+1.0/9)) {
		t.Fatalf("attacker holding its own flag: hold %.3f, want %.3f", s.Hold, 5*(1+1.0/9))
	}
}

func TestMirrorDownNeverBeatsUp(t *testing.T) {
	w := newWorld(t)
	w.attackers(w.teams[0:3]...)
	down, exploited := w.teams[10], w.teams[11]
	w.flag(down, 0, false) // nobody stole it, but the service was down
	w.flag(exploited, 3, true)
	r := scoreMirror(w.in)
	d, e := r.Teams[down], r.Teams[exploited]
	if d.Hold != 0 || d.Up != 0 {
		t.Fatalf("down team scored hold %.3f up %.3f", d.Hold, d.Up)
	}
	if e.AD <= d.AD {
		t.Fatalf("up-and-exploited (%.3f) must beat down (%.3f)", e.AD, d.AD)
	}
	for id, s := range r.Teams {
		if s.Hold < 0 || s.Up < 0 || s.Steal < 0 {
			t.Fatalf("negative stream for %s: %+v", id, s)
		}
	}
}

func TestMirrorLifeRuleVoidsHoldOnAnyRedTick(t *testing.T) {
	w := newWorld(t)
	w.in.Current = 6
	w.attackers(w.teams[0:4]...)
	honest, filter := w.teams[10], w.teams[11]
	for _, team := range []uuid.UUID{honest, filter} {
		for r := 1; r <= 5; r++ {
			w.in.SLA[slaKey{team, w.service, r}] = slaTick{Status: models.SLAOk}
		}
	}
	// the filter trips one check in the flag's five-tick life
	w.in.SLA[slaKey{filter, w.service, 3}] = slaTick{Status: models.SLAFaulty, Step: "havoc"}
	w.in.Flags = append(w.in.Flags,
		mirrorFlag{ID: uuid.New(), Team: honest, Service: w.service, Tick: 1, ValidUntil: 5},
		mirrorFlag{ID: uuid.New(), Team: filter, Service: w.service, Tick: 1, ValidUntil: 5})
	r := scoreMirror(w.in)
	if r.Teams[filter].Hold != 0 {
		t.Fatalf("one red tick must void the flag's hold, got %.3f", r.Teams[filter].Hold)
	}
	if r.Teams[honest].AD <= r.Teams[filter].AD {
		t.Fatalf("honest patch (%.3f) must beat the blanket filter (%.3f)", r.Teams[honest].AD, r.Teams[filter].AD)
	}

	// a flag not read back on one of its ticks is not held either
	w.in.Flags[1].Unread = []int{4}
	w.in.SLA[slaKey{filter, w.service, 3}] = slaTick{Status: models.SLAOk}
	if scoreMirror(w.in).Teams[filter].Hold != 0 {
		t.Fatal("a flag missing on one tick of its life was held")
	}
}

func TestMirrorUnsettledFlagsWait(t *testing.T) {
	w := newWorld(t)
	w.in.Current = 1 // tick 1 is closing: its flags are still submittable
	w.attackers(w.teams[0:2]...)
	w.flag(w.teams[9], 0, true)
	if h := scoreMirror(w.in).Teams[w.teams[9]].Hold; h != 0 {
		t.Fatalf("hold paid before the flag's submit window closed: %.3f", h)
	}
	w.in.Final = true // game over: judged on the ticks that exist
	if h := scoreMirror(w.in).Teams[w.teams[9]].Hold; !near(h, 4) {
		t.Fatalf("final settlement hold %.3f, want 4", h)
	}
}

func TestMirrorOutbreakWave(t *testing.T) {
	w := newWorld(t)
	patched, unpatched := w.teams[3], w.teams[4]
	w.flag(patched, 0, true)
	w.flag(unpatched, 0, true)
	w.in.Flags[1].BotStolen = true

	// nobody cracked the store and no wave: defense pays nothing
	r := scoreMirror(w.in)
	if r.Teams[patched].Hold != 0 || r.Teams[unpatched].Hold != 0 {
		t.Fatal("hold paid on a store with no attackers and no wave")
	}
	// the wave runs and the nop canary confirms it: A floors at 1
	w.in.NopStolen[w.st()] = true
	r = scoreMirror(w.in)
	if !near(r.Teams[patched].Hold, 2) {
		t.Fatalf("patched team under a wave: hold %.3f, want 1 x (1 + 1/1)", r.Teams[patched].Hold)
	}
	if r.Teams[unpatched].Hold != 0 {
		t.Fatal("a flag the wave read was still held")
	}
	// without the canary the wave's verdicts are discarded
	w.in.NopStolen[w.st()] = false
	r = scoreMirror(w.in)
	if r.Teams[unpatched].Hold != 0 || r.Teams[patched].Hold != 0 {
		t.Fatal("discarded wave still floored A or marked flags lost")
	}
	// real attackers: the floor never changes the math
	w.in.NopStolen[w.st()] = true
	w.attackers(w.teams[0:3]...)
	if h := scoreMirror(w.in).Teams[patched].Hold; !near(h, 3*2) {
		t.Fatalf("wave with three real attackers: hold %.3f, want 6", h)
	}
}

func TestMirrorOrganizerFaults(t *testing.T) {
	w := newWorld(t)
	w.attackers(w.teams[0:2]...)
	victims := w.teams[5:9]
	for _, v := range victims {
		w.flag(v, 0, false) // down and unread: credit must restore both up and hold
	}
	// the nop fails the same step: 4 teams >= 3 => our fault, credited
	w.in.SLA[slaKey{w.nop, w.service, 1}] = slaTick{Status: models.SLADown, Step: "putflag"}
	r := scoreMirror(w.in)
	for _, v := range victims {
		if r.Effective[slaKey{v, w.service, 1}] != models.SLAError || r.Teams[v].Up == 0 {
			t.Fatalf("mass fault with the nop canary not credited for %s", v)
		}
		if !near(r.Teams[v].Hold, 2*(1+1.0/4)) {
			t.Fatalf("credited tick must not cost the hold: %.3f", r.Teams[v].Hold)
		}
	}
	// two teams failing with the nop is not enough
	w.in.MassFault = 5
	if r := scoreMirror(w.in); r.Teams[victims[0]].Up != 0 {
		t.Fatal("credited below the mass-fault threshold")
	}
	// an admin re-credit always applies
	w.in.Credits = []slaCredit{{Service: w.service, Team: &victims[0], From: 1, To: 1}}
	if r := scoreMirror(w.in); r.Teams[victims[0]].Up == 0 || r.Teams[victims[1]].Up != 0 {
		t.Fatal("admin credit not applied to exactly its team")
	}
	// an outbreak crash on the box is ours too
	w.in.Credits = nil
	w.in.BotCrashed[slaKey{victims[1], w.service, 1}] = true
	if r := scoreMirror(w.in); r.Teams[victims[1]].Up == 0 {
		t.Fatal("outbreak crash not credited")
	}
}

func TestMirrorUpAndRecovering(t *testing.T) {
	w := newWorld(t)
	w.in.Stores[w.service] = 2
	a, b, c := w.teams[0], w.teams[1], w.teams[2]
	w.in.SLA[slaKey{a, w.service, 1}] = slaTick{Status: models.SLAOk}
	w.in.SLA[slaKey{b, w.service, 1}] = slaTick{Status: models.SLARecovering}
	w.in.RecShare[slaKey{b, w.service, 1}] = 0.6
	w.in.SLA[slaKey{c, w.service, 1}] = slaTick{Status: models.SLAOk}
	r := scoreMirror(w.in)
	full := math.Sqrt(15) * 2
	if !near(r.Teams[a].Up, full) || !near(r.Teams[b].Up, full*0.6) || !near(r.Teams[c].Up, full) {
		t.Fatalf("up: ok %.3f recovering %.3f second ok %.3f", r.Teams[a].Up, r.Teams[b].Up, r.Teams[c].Up)
	}
	if !near(r.Breakdown[teamStore{a, w.service, 1}].Up, full/2) {
		t.Fatal("up not split evenly across stores")
	}
}

func TestMirrorRelativeMerge(t *testing.T) {
	w := newWorld(t)
	for i, id := range w.teams {
		w.in.Steal[teamStore{id, w.service, 0}] = float64(i * 10) // AD 0..140
		w.in.Koth[id] = 0
	}
	w.in.Koth[w.teams[3]] = 50
	r := scoreMirror(w.in)
	if !near(r.Teams[w.teams[14]].Total, 700) {
		t.Fatalf("AD leader total %.3f, want 700 from the AD board", r.Teams[w.teams[14]].Total)
	}
	if !near(r.Teams[w.teams[0]].Total, 0) {
		t.Fatalf("last team total %.3f, want 0", r.Teams[w.teams[0]].Total)
	}
	if !near(r.Teams[w.teams[3]].Total, 1000*(0.7*30.0/140+0.3*1)) {
		t.Fatalf("koth leader total %.3f", r.Teams[w.teams[3]].Total)
	}
	if _, ok := r.Teams[w.nop]; ok {
		t.Fatal("the nop team was scored")
	}
}

func TestMirrorAttackerBonus(t *testing.T) {
	w := newWorld(t)
	w.in.Bonus = 1.5
	w.attackers(w.teams[0:4]...)
	w.flag(w.teams[10], 0, true)
	w.flag(w.teams[11], 4, true)
	r := scoreMirror(w.in)
	if !near(r.Teams[w.teams[0]].Steal, 1.5) {
		t.Fatalf("attacker bonus %.3f, want 1.5 per active store-tick", r.Teams[w.teams[0]].Steal)
	}
	if !near(r.Teams[w.teams[10]].Hold, 4*1.5*2) {
		t.Fatalf("hold with bonus weight %.3f, want 12", r.Teams[w.teams[10]].Hold)
	}
}

func TestMirrorJeopardyThreeWayMerge(t *testing.T) {
	w := newWorld(t)
	w.in.WeightAD, w.in.WeightKoth, w.in.WeightJeopardy = 0.6, 0.25, 0.15
	w.in.Jeopardy = map[uuid.UUID]float64{w.nop: 9999} // the nop never enters the board
	for i, id := range w.teams {
		w.in.Steal[teamStore{id, w.service, 0}] = float64(i * 10) // AD 0..140
		w.in.Koth[id] = float64((14 - i) * 2)                     // koth 28..0
		if i != 5 {
			w.in.Jeopardy[id] = float64(i * 100) // team 5 never solved: missing scores 0
		}
	}
	// jeopardy 0..1400 with team 5 at 0: last team 0, leader 1400
	r := scoreMirror(w.in)
	want := func(i int, ad, ko, je float64) {
		t.Helper()
		total := 1000 * (0.6*ad/140 + 0.25*ko/28 + 0.15*je/1400)
		if s := r.Teams[w.teams[i]]; !near(s.Total, total) || s.Jeopardy != je {
			t.Fatalf("team %d total %.3f jeopardy %.0f, want %.3f and %.0f", i, s.Total, s.Jeopardy, total, je)
		}
	}
	want(14, 140, 0, 1400) // AD and jeopardy leader, koth last: 600 + 0 + 150
	want(0, 0, 28, 0)      // koth leader only
	want(5, 50, 18, 0)     // missing from the jeopardy map
	want(7, 70, 14, 700)   // halfway on all three boards: 500
	for _, s := range r.Teams {
		if s.Total < 0 {
			t.Fatalf("negative total %.3f", s.Total)
		}
	}
	if _, ok := r.Teams[w.nop]; ok {
		t.Fatal("the nop team was scored")
	}
}

func TestMirrorJeopardyWeightZeroIsUnchanged(t *testing.T) {
	w := newWorld(t)
	for i, id := range w.teams {
		w.in.Steal[teamStore{id, w.service, 0}] = float64(i*13%7) + 0.1*float64(i)
		w.in.Koth[id] = float64(i * i % 11)
	}
	before := scoreMirror(w.in)
	ads, kos := []float64{}, []float64{}
	for _, s := range before.Teams {
		ads = append(ads, s.AD)
		kos = append(kos, s.Koth)
	}
	adLow, adTop := lowTop(ads)
	koLow, koTop := lowTop(kos)

	// jeopardy points loaded but weighted 0, or not loaded at all: the old formula, bit for bit
	for _, jeopardy := range []map[uuid.UUID]float64{nil, {w.teams[0]: 5000, w.teams[3]: 120}} {
		w.in.Jeopardy = jeopardy
		r := scoreMirror(w.in)
		for id, s := range r.Teams {
			old := 1000 * (0.7*relative(s.AD, adLow, adTop) + 0.3*relative(s.Koth, koLow, koTop))
			if s.Total != old || s.Total != before.Teams[id].Total {
				t.Fatalf("weight 0 total %v, want %v", s.Total, old)
			}
		}
	}
}

func TestSharesAddUpToTotal(t *testing.T) {
	w := newWorld(t)
	w.in.WeightAD, w.in.WeightKoth, w.in.WeightJeopardy = 0.6, 0.25, 0.15
	w.in.Jeopardy = map[uuid.UUID]float64{}
	for i, id := range w.teams {
		w.in.Steal[teamStore{id, w.service, 0}] = float64(i * 10)
		w.in.Koth[id] = float64((14 - i) * 2)
		w.in.Jeopardy[id] = float64(i * 100)
	}
	r := scoreMirror(w.in)
	ids := make([]uuid.UUID, 0, len(r.Teams))
	ad, ko, total := []float64{}, []float64{}, []float64{}
	for id, s := range r.Teams {
		ids = append(ids, id)
		ad, ko, total = append(ad, s.AD), append(ko, s.Koth), append(total, s.Total)
	}
	adS, koS, jeS := Shares(0.6, 0.25, ad, ko, total)
	for i, id := range ids {
		s := r.Teams[id]
		if !near(adS[i], 600*s.AD/140) || !near(koS[i], 250*s.Koth/28) || !near(jeS[i], 150*s.Jeopardy/1400) {
			t.Fatalf("shares %.3f %.3f %.3f for ad %.0f koth %.0f jeopardy %.0f", adS[i], koS[i], jeS[i], s.AD, s.Koth, s.Jeopardy)
		}
		if !near(adS[i]+koS[i]+jeS[i], s.Total) {
			t.Fatalf("shares sum %.3f, total %.3f", adS[i]+koS[i]+jeS[i], s.Total)
		}
	}
}

func TestSharesJeopardyRemainderIgnoresStoredRounding(t *testing.T) {
	ad, koth := []float64{0, 50, 100}, []float64{0, 0, 10}
	for _, d := range []float64{0.0004, -0.0004} {
		_, _, je := Shares(0.65, 0.3, ad, koth, []float64{d, 325 + d, 950 + d})
		for i, v := range je {
			if v != 0 || math.Signbit(v) {
				t.Fatalf("residue %v: jeopardy share[%d] = %v, want 0", d, i, v)
			}
		}
	}
	if _, _, je := Shares(0.65, 0.3, ad, koth, []float64{-650, 0, 950.01}); !near(je[2], 0.01) {
		t.Fatalf("a real jeopardy share was snapped: %v", je[2])
	}
}
