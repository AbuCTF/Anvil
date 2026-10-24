package game

import (
	"math"
	"sort"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// h7 mirror (finals-research/06 section 3.2). per flag store and tick, settled when
// the flag expires, never negative:
//   STEAL += 1 + 1/c                     per flag stolen, c = teams that stole it
//   HOLD  += A x (1 + 1/h)               per flag nobody stole and that read back every
//                                        tick of its life while the service was green
//   UP    += sqrt(N) x q per store-tick  q = 1 OK or ORG_FAULT, readable share if RECOVERING
// then TOTAL = 1000 x (wAD x AD_rel + wK x KOTH_rel + wJ x JEOPARDY_rel), each
// relative to its median team. wJ = 0 leaves the jeopardy board out entirely.

type slaKey struct {
	team    uuid.UUID
	service uuid.UUID
	tick    int
}

type storeTick struct {
	service uuid.UUID
	store   int
	tick    int
}

type teamStore struct {
	team    uuid.UUID
	service uuid.UUID
	store   int
}

type mirrorFlag struct {
	ID         uuid.UUID
	Team       uuid.UUID
	Service    uuid.UUID
	Store      int
	Tick       int
	ValidUntil int
	Unread     []int // life ticks on which no getflag read it back
	Captors    int   // distinct non-nop teams that stole it
	BotStolen  bool  // the outbreak wave read it (before the nop canary)
}

type slaTick struct {
	Status models.SLAStatus
	Step   string
}

type slaCredit struct {
	Service  uuid.UUID
	Team     *uuid.UUID // nil = every team
	From, To int
	Reason   string
}

type mirrorInput struct {
	N          int
	Current    int                // the tick being closed; flags whose submit window ended before it are settled
	Final      bool               // game over: settle everything on the ticks that exist
	Teams      map[uuid.UUID]bool // active teams -> is nop
	Stores     map[uuid.UUID]int  // service -> flag stores
	Flags      []mirrorFlag
	SLA        map[slaKey]slaTick
	RecShare   map[slaKey]float64 // readable share of window flags on a RECOVERING tick
	Credits    []slaCredit
	BotCrashed map[slaKey]bool
	NopStolen  map[storeTick]bool // the wave read the nop's flag: its verdicts stand
	Attackers  map[storeTick]map[uuid.UUID]bool
	Steal      map[teamStore]float64 // settled STEAL already summed in sql
	Koth       map[uuid.UUID]float64
	Jeopardy   map[uuid.UUID]float64 // jeopardy solve points; a missing team scores 0
	MassFault  int
	Bonus      float64 // attacker bonus w; 0 = off
	WeightAD   float64
	WeightKoth float64

	WeightJeopardy float64
}

type mirrorScore struct {
	Steal, Hold, Up, AD, Koth, Jeopardy, Total float64
}

type mirrorBreakdown struct {
	Steal, Hold, Up      float64
	FlagsHeld, FlagsDone int
}

type mirrorResult struct {
	Teams     map[uuid.UUID]*mirrorScore
	Breakdown map[teamStore]*mirrorBreakdown
	Effective map[slaKey]models.SLAStatus
}

func green(s models.SLAStatus) bool {
	return s == models.SLAOk || s == models.SLARecovering || s == models.SLAError
}

// effectiveSLA applies organizer credit to the raw verdicts: checker faults are
// ERROR already; admin re-credits, outbreak crashes and mass failures shared with
// the nop canary become ERROR too. ERROR counts as green for UP and the life rule.
func effectiveSLA(in *mirrorInput) (map[slaKey]models.SLAStatus, map[slaKey]string) {
	eff := make(map[slaKey]models.SLAStatus, len(in.SLA))
	why := map[slaKey]string{}
	for k, v := range in.SLA {
		eff[k] = v.Status
		if v.Status == models.SLAError {
			why[k] = "checker fault"
		}
	}
	for k := range in.BotCrashed {
		if s, ok := eff[k]; ok && !green(s) {
			eff[k] = models.SLAError
			why[k] = "outbreak wave crashed the service"
		}
	}
	for _, c := range in.Credits {
		for k, s := range eff {
			if k.service == c.Service && k.tick >= c.From && k.tick <= c.To && (c.Team == nil || *c.Team == k.team) && !green(s) {
				eff[k] = models.SLAError
				why[k] = c.Reason
			}
		}
	}
	if in.MassFault > 0 {
		var nops []uuid.UUID
		for id, nop := range in.Teams {
			if nop {
				nops = append(nops, id)
			}
		}
		// a failure the untouched nop box shares, hitting several teams on the same
		// step at once, is the checker or our network, not the teams
		type failKey struct {
			service uuid.UUID
			tick    int
			step    string
		}
		failing := map[failKey][]slaKey{}
		for k, v := range in.SLA {
			if !in.Teams[k.team] && !green(v.Status) {
				fk := failKey{k.service, k.tick, v.Step}
				failing[fk] = append(failing[fk], k)
			}
		}
		for _, nop := range nops {
			for k, v := range in.SLA {
				if k.team != nop || green(v.Status) {
					continue
				}
				keys := failing[failKey{k.service, k.tick, v.Step}]
				if len(keys) >= in.MassFault {
					for _, tk := range keys {
						eff[tk] = models.SLAError
						why[tk] = "shared failure with the nop box"
					}
				}
			}
		}
	}
	return eff, why
}

func scoreMirror(in *mirrorInput) mirrorResult {
	res := mirrorResult{
		Teams:     map[uuid.UUID]*mirrorScore{},
		Breakdown: map[teamStore]*mirrorBreakdown{},
	}
	for id, nop := range in.Teams {
		if !nop {
			res.Teams[id] = &mirrorScore{}
		}
	}
	bd := func(k teamStore) *mirrorBreakdown {
		b := res.Breakdown[k]
		if b == nil {
			b = &mirrorBreakdown{}
			res.Breakdown[k] = b
		}
		return b
	}
	eff, _ := effectiveSLA(in)
	res.Effective = eff

	// STEAL comes pre-summed per (attacker, store) over settled flags
	for k, v := range in.Steal {
		if s := res.Teams[k.team]; s != nil {
			s.Steal += v
			bd(k).Steal += v
		}
	}

	settled := func(f mirrorFlag) bool { return in.Final || f.ValidUntil < in.Current }
	lastTick := func(f mirrorFlag) int {
		if in.Final && f.ValidUntil >= in.Current {
			return in.Current
		}
		return f.ValidUntil
	}

	// a held flag: nobody stole it, the wave didn't read it (when the nop canary
	// confirms the wave), and every tick of its life was green and read it back
	type candidate struct {
		flag mirrorFlag
		a    int
	}
	held := map[storeTick][]candidate{}
	done := map[storeTick]bool{}
	for _, f := range in.Flags {
		if in.Teams[f.Team] || !settled(f) {
			continue
		}
		st := storeTick{f.Service, f.Store, f.Tick}
		done[st] = true
		wave := in.NopStolen[st]
		if res.Teams[f.Team] != nil {
			bd(teamStore{f.Team, f.Service, f.Store}).FlagsDone++
		}
		if f.Captors > 0 || (wave && f.BotStolen) {
			continue
		}
		end := lastTick(f)
		unread := map[int]bool{}
		for _, r := range f.Unread {
			unread[r] = true
		}
		ok := true
		for r := f.Tick; r <= end; r++ {
			// a credited tick counts as read: our fault never costs a hold
			st := eff[slaKey{f.Team, f.Service, r}]
			if !green(st) || (unread[r] && st != models.SLAError) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		a := len(in.Attackers[st])
		if in.Attackers[st][f.Team] {
			a--
		}
		if wave && a < 1 {
			a = 1 // a store nobody cracked still pays defense once the wave runs on it
		}
		if a > 0 {
			held[st] = append(held[st], candidate{f, a})
		}
	}
	w := 1.0
	if in.Bonus > 0 {
		w = in.Bonus
	}
	for _, cs := range held {
		h := float64(len(cs))
		for _, c := range cs {
			pts := float64(c.a) * w * (1 + 1/h)
			if s := res.Teams[c.flag.Team]; s != nil {
				s.Hold += pts
				b := bd(teamStore{c.flag.Team, c.flag.Service, c.flag.Store})
				b.Hold += pts
				b.FlagsHeld++
			}
		}
	}
	// the ecsc-style attacker bonus: w per store-tick an attacker was active on
	if in.Bonus > 0 {
		for st, atk := range in.Attackers {
			if !done[st] {
				continue
			}
			for team := range atk {
				if s := res.Teams[team]; s != nil {
					s.Steal += in.Bonus
					bd(teamStore{team, st.service, st.store}).Steal += in.Bonus
				}
			}
		}
	}

	n := in.N
	if n < 1 {
		n = 1
	}
	perStore := math.Sqrt(float64(n))
	for k, s := range eff {
		score := res.Teams[k.team]
		if score == nil || k.tick > in.Current {
			continue
		}
		var q float64
		switch s {
		case models.SLAOk, models.SLAError:
			q = 1
		case models.SLARecovering:
			q = 0.5
			if share, ok := in.RecShare[k]; ok {
				q = share
			}
		default:
			continue
		}
		stores := in.Stores[k.service]
		if stores < 1 {
			stores = 1
		}
		up := perStore * q * float64(stores)
		score.Up += up
		for i := 0; i < stores; i++ {
			bd(teamStore{k.team, k.service, i}).Up += up / float64(stores)
		}
	}

	ads := make([]float64, 0, len(res.Teams))
	kos := make([]float64, 0, len(res.Teams))
	jes := make([]float64, 0, len(res.Teams))
	for id, s := range res.Teams {
		s.AD = s.Steal + s.Hold + s.Up
		s.Koth = in.Koth[id]
		s.Jeopardy = in.Jeopardy[id]
		ads = append(ads, s.AD)
		kos = append(kos, s.Koth)
		jes = append(jes, s.Jeopardy)
	}
	adMed, adTop := medianTop(ads)
	koMed, koTop := medianTop(kos)
	jeMed, jeTop := medianTop(jes)
	for _, s := range res.Teams {
		s.Total = 1000 * (in.WeightAD*relative(s.AD, adMed, adTop) + in.WeightKoth*relative(s.Koth, koMed, koTop) +
			in.WeightJeopardy*relative(s.Jeopardy, jeMed, jeTop))
	}
	return res
}

func medianTop(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	mid := len(s) / 2
	med := s[mid]
	if len(s)%2 == 0 {
		med = (s[mid-1] + s[mid]) / 2
	}
	return med, s[len(s)-1]
}

// relative scales a board from its median team (0) to its leader (1)
func relative(x, med, top float64) float64 {
	return (x - med) / math.Max(top-med, 1)
}

// Shares cuts finals totals into what each board earned, 1000 x weight x relative
// over one field. jeopardy is the rest, so the three parts always add up to total.
func Shares(wAD, wKoth float64, ad, koth, total []float64) (adShare, kothShare, jeShare []float64) {
	adMed, adTop := medianTop(ad)
	koMed, koTop := medianTop(koth)
	adShare = make([]float64, len(ad))
	kothShare = make([]float64, len(ad))
	jeShare = make([]float64, len(ad))
	for i := range ad {
		adShare[i] = 1000 * wAD * relative(ad[i], adMed, adTop)
		kothShare[i] = 1000 * wKoth * relative(koth[i], koMed, koTop)
		jeShare[i] = total[i] - adShare[i] - kothShare[i]
	}
	return adShare, kothShare, jeShare
}
