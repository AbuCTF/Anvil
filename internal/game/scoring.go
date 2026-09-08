package game

import (
	"math"
	"sort"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// attackContribution is the offense value of one captured flag, split across
// every team that captured it — a widely-stolen flag is worth little.
func attackContribution(base float64, captors int) float64 {
	if captors <= 0 {
		return 0
	}
	return base / float64(captors)
}

// defensePenalty is the loss for one of your flags being captured, sublinear in
// the number of attackers so mass theft is not linearly punishing.
func defensePenalty(factor float64, captors int) float64 {
	if captors <= 0 {
		return 0
	}
	return factor * math.Sqrt(float64(captors))
}

// slaTickPoints is the SLA reward for one service on one tick, scaled by field size.
func slaTickPoints(points float64, numTeams int, status models.SLAStatus) float64 {
	if numTeams < 1 {
		numTeams = 1
	}
	scale := points * math.Sqrt(float64(numTeams))
	switch status {
	case models.SLAOk:
		return scale
	case models.SLARecovering:
		return scale * 0.5
	default:
		return 0
	}
}

type teamHold struct {
	Team uuid.UUID
	Held int
}

// kothRankPoints ranks teams by hold time (descending) and awards the rank
// table (e.g. 12/7/4/2/1). Teams with no hold time score nothing.
func kothRankPoints(rankTable []int, holds []teamHold) map[uuid.UUID]float64 {
	sorted := make([]teamHold, len(holds))
	copy(sorted, holds)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Held > sorted[j].Held })

	out := make(map[uuid.UUID]float64)
	for i, th := range sorted {
		if i >= len(rankTable) || th.Held <= 0 {
			break
		}
		out[th.Team] = float64(rankTable[i])
	}
	return out
}
