package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// attackInfo is the enowars attack.json shape, so stock exploit farms read it as is:
// services -> team address -> tick -> flag variant -> attack infos.
type attackInfo struct {
	AvailableTeams []string                                       `json:"availableTeams"`
	Services       map[string]map[string]map[int]map[int][]string `json:"services"`
}

// AttackInfo publishes the flag ids of every still-valid flag. A flag id is whatever
// the checker returned from putflag (a username, an object id) and is public by design.
func (h *GameHandler) AttackInfo(c *gin.Context) {
	if h.off(c) {
		return
	}
	now := time.Now()
	h.attackMu.Lock()
	if h.attackBody != nil && now.Before(h.attackExpires) {
		body := h.attackBody
		h.attackMu.Unlock()
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
		return
	}
	h.attackMu.Unlock()

	ctx, cancel := context.WithTimeout(c.Request.Context(), arenaQueryTimeout)
	defer cancel()
	body, err := h.loadAttackInfo(ctx)
	if err != nil {
		h.logger.Error("attack info", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.attackMu.Lock()
	h.attackBody, h.attackExpires = body, now.Add(arenaLiveCacheTTL)
	h.attackMu.Unlock()
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (h *GameHandler) loadAttackInfo(ctx context.Context) ([]byte, error) {
	var current int
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&current); err != nil {
		return nil, err
	}

	out := attackInfo{AvailableTeams: []string{}, Services: map[string]map[string]map[int]map[int][]string{}}

	// a team is unavailable once every service it runs was DOWN on the last checked
	// tick, so farms stop wasting their tick on it (enowars does the same)
	teams, err := h.db.Pool.Query(ctx,
		`SELECT host(t.vulnbox_ip) FROM game_teams t
		 WHERE t.status = 'active' AND t.vulnbox_ip IS NOT NULL AND (t.is_nop OR `+publicGameTeamSQL("t")+`)
		   AND NOT EXISTS (
		     SELECT 1 FROM game_sla_checks s
		     WHERE s.team_id = t.id AND s.tick_number = (SELECT MAX(tick_number) FROM game_sla_checks)
		     HAVING bool_and(s.status = 'DOWN'))
		 ORDER BY t.team_number NULLS LAST, t.name`)
	if err != nil {
		return nil, err
	}
	for teams.Next() {
		var addr string
		if err := teams.Scan(&addr); err != nil {
			teams.Close()
			return nil, err
		}
		out.AvailableTeams = append(out.AvailableTeams, addr)
	}
	teams.Close()
	if err := teams.Err(); err != nil {
		return nil, err
	}

	rows, err := h.db.Pool.Query(ctx,
		`SELECT s.slug, host(t.vulnbox_ip), f.tick_number, f.store_index, f.attack_info
		 FROM game_flags f
		 JOIN game_teams t ON t.id = f.team_id
		 JOIN game_services s ON s.id = f.service_id
		 WHERE f.planted_at IS NOT NULL AND f.attack_info IS NOT NULL AND f.valid_until_tick >= $1
		   AND t.status = 'active' AND t.vulnbox_ip IS NOT NULL AND (t.is_nop OR `+publicGameTeamSQL("t")+`)
		 ORDER BY s.slug, t.team_number NULLS LAST, f.tick_number, f.store_index`, current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var service, addr, info string
		var tick, variant int
		if err := rows.Scan(&service, &addr, &tick, &variant, &info); err != nil {
			return nil, err
		}
		byTeam := out.Services[service]
		if byTeam == nil {
			byTeam = map[string]map[int]map[int][]string{}
			out.Services[service] = byTeam
		}
		byTick := byTeam[addr]
		if byTick == nil {
			byTick = map[int]map[int][]string{}
			byTeam[addr] = byTick
		}
		byVariant := byTick[tick]
		if byVariant == nil {
			byVariant = map[int][]string{}
			byTick[tick] = byVariant
		}
		byVariant[variant] = append(byVariant[variant], info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
