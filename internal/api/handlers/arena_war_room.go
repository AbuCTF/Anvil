package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// warRoom is the projector view: service health per team, breaches without attacker
// names, hill holders and the event feed. Tick-derived parts run on the public view
// delay and stop at the freeze; hill holders stay live because the hills expose them.
type warRoom struct {
	Tick     int            `json:"tick"`
	ViewTick int            `json:"view_tick"`
	Frozen   bool           `json:"frozen"`
	Teams    []warTeam      `json:"teams"`
	Services []warService   `json:"services"`
	Grid     []warCell      `json:"grid"`
	Hills    []warHill      `json:"hills"`
	Events   []warRoomEvent `json:"events"`
}

type warTeam struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	IsNop bool   `json:"is_nop"`
}

type warService struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

type warCell struct {
	TeamID      string `json:"team_id"`
	ServiceSlug string `json:"service_slug"`
	Status      string `json:"status"` // up | mumble | down
	BreachFlash bool   `json:"breach_flash"`
	IsOwn       bool   `json:"is_own"`
}

type warHill struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	HolderName  *string `json:"holder_name"`
	HeldTicks   int     `json:"held_ticks"`
	RoundEndsIn int     `json:"round_ends_in"`
	IsActive    bool    `json:"is_active"`
}

type warRoomEvent struct {
	Kind     string `json:"kind"` // first_blood | shield_up | crown | advisory | hill_open | hill_close
	TeamName string `json:"team_name,omitempty"`
	Service  string `json:"service,omitempty"`
	Hill     string `json:"hill,omitempty"`
	Text     string `json:"text,omitempty"`
	Tick     int    `json:"tick"`
	TS       string `json:"ts"`
}

const warRoomEvents = 40

// WarRoom serves GET /api/v1/arena/war-room.
func (h *GameHandler) WarRoom(c *gin.Context) {
	if h.off(c) {
		return
	}
	state, err := game.ReadState(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("war room mode", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to load war room"})
		return
	}
	if state == game.StateLegacy {
		c.JSON(http.StatusNotFound, gin.H{"error": "war room is a finals view"})
		return
	}

	room, cacheStatus, err := h.warRoomSnapshot(c.Request.Context())
	if err != nil && room == nil {
		h.logger.Error("war room", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "war room request timed out"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to load war room"})
		return
	}
	if err != nil {
		h.logger.Warn("serving stale war room", zap.Error(err))
	}

	// Never allow an intermediary to cache a team-personalized response. The
	// in-process snapshot still protects PostgreSQL from the polling fan-out.
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization")
	c.Header("X-Anvil-Cache", cacheStatus)
	out := personalizeWarRoom(room, h.viewerTeam(c.Request.Context(), c))
	c.JSON(http.StatusOK, out)
}

// warRoomSnapshot coalesces concurrent cache misses. The leader uses a bounded
// background context so one browser disconnect cannot cancel the shared refresh.
func (h *GameHandler) warRoomSnapshot(ctx context.Context) (*warRoom, string, error) {
	for {
		if room, ok := h.cachedWarRoom(time.Now(), false); ok {
			return room, "HIT", nil
		}

		h.warFlightMu.Lock()
		if h.warFlight == nil {
			flight := &warRoomFlight{done: make(chan struct{})}
			h.warFlight = flight
			h.warFlightMu.Unlock()

			loadCtx, cancel := context.WithTimeout(context.Background(), arenaQueryTimeout)
			var loaded *warRoom
			var err error
			if h.warLoad != nil {
				loaded, err = h.warLoad(loadCtx)
			} else {
				loaded, err = h.loadWarRoom(loadCtx)
			}
			cancel()
			if err == nil && loaded == nil {
				err = errors.New("war room loader returned no snapshot")
			}
			if err == nil {
				h.warMu.Lock()
				h.warRoom = loaded
				h.warExpires = time.Now().Add(arenaLiveCacheTTL)
				h.warMu.Unlock()
			}

			h.warFlightMu.Lock()
			flight.err = err
			close(flight.done)
			h.warFlight = nil
			h.warFlightMu.Unlock()
			if err != nil {
				if stale, ok := h.cachedWarRoom(time.Now(), true); ok {
					return stale, "STALE", err
				}
			}
			return loaded, "MISS", err
		}
		flight := h.warFlight
		h.warFlightMu.Unlock()

		select {
		case <-flight.done:
			if flight.err != nil {
				if stale, ok := h.cachedWarRoom(time.Now(), true); ok {
					return stale, "STALE", flight.err
				}
				return nil, "", flight.err
			}
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
}

func (h *GameHandler) cachedWarRoom(now time.Time, allowStale bool) (*warRoom, bool) {
	h.warMu.Lock()
	defer h.warMu.Unlock()
	if h.warRoom == nil || (!allowStale && !now.Before(h.warExpires)) ||
		(allowStale && !now.Before(h.warExpires.Add(arenaStaleTTL))) {
		return nil, false
	}
	return h.warRoom, true
}

// personalizeWarRoom clones the only viewer-specific slice so an authenticated
// response cannot mutate the shared snapshot or leak its marker to the next team.
func personalizeWarRoom(room *warRoom, own string) warRoom {
	out := *room
	out.Grid = make([]warCell, len(room.Grid))
	for i, cell := range room.Grid {
		cell.IsOwn = own != "" && cell.TeamID == own
		out.Grid[i] = cell
	}
	return out
}

func (h *GameHandler) viewerTeam(ctx context.Context, c *gin.Context) string {
	uid := middleware.GetUserID(c)
	if uid == nil {
		return ""
	}
	team, ok, err := game.TeamForUser(ctx, h.db, *uid)
	if err != nil || !ok {
		return ""
	}
	return team.String()
}

func (h *GameHandler) loadWarRoom(ctx context.Context) (*warRoom, error) {
	room := &warRoom{Teams: []warTeam{}, Services: []warService{}, Grid: []warCell{}, Hills: []warHill{}, Events: []warRoomEvent{}}
	pool := h.db.Pool

	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&room.Tick); err != nil {
		return nil, err
	}
	view, _, err := game.PublicViewTick(ctx, h.db)
	if err != nil {
		return nil, err
	}
	frozen, err := game.IsFrozen(ctx, h.db)
	if err != nil {
		return nil, err
	}
	if frozen {
		// breaches stop at the last tick that started before the freeze
		var last int
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks
			 WHERE started_at < (SELECT (value #>> '{}')::timestamptz FROM platform_settings WHERE key = 'event.freeze_at')`).Scan(&last); err != nil {
			return nil, err
		}
		view = min(view, last)
	}
	room.ViewTick, room.Frozen = view, frozen

	rows, err := pool.Query(ctx,
		`SELECT gt.id::text, gt.name, gt.is_nop FROM game_teams gt
		 WHERE gt.status = 'active' AND `+publicGameTeamSQL("gt")+`
		 ORDER BY gt.is_nop, gt.team_number NULLS LAST, gt.name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t warTeam
		if err := rows.Scan(&t.ID, &t.Name, &t.IsNop); err != nil {
			rows.Close()
			return nil, err
		}
		room.Teams = append(room.Teams, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx,
		`SELECT slug, name, COALESCE(port, 0) FROM game_services
		 WHERE enabled = true AND (release_at IS NULL OR release_at <= NOW())
		 ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s warService
		if err := rows.Scan(&s.Slug, &s.Name, &s.Port); err != nil {
			rows.Close()
			return nil, err
		}
		room.Services = append(room.Services, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Health at the delayed tick. Cross joining the visible teams and released
	// services keeps the matrix rectangular even before the first checker result.
	// A checker ERROR is ours, so it shows as up; a missing result shows as down.
	rows, err = pool.Query(ctx,
		`SELECT gt.id::text, gs.slug,
		        CASE
		          WHEN sc.status IN ('OK', 'ERROR') THEN 'up'
		          WHEN sc.status IN ('FAULTY', 'FLAG_NOT_FOUND', 'RECOVERING') THEN 'mumble'
		          ELSE 'down'
		        END,
		        EXISTS (SELECT 1 FROM game_captures cp
		                WHERE cp.victim_team_id = gt.id AND cp.service_id = gs.id AND cp.tick_number = $1)
		 FROM game_teams gt
		 CROSS JOIN game_services gs
		 LEFT JOIN game_sla_checks sc
		   ON sc.team_id = gt.id AND sc.service_id = gs.id AND sc.tick_number = $1
		 WHERE gt.status = 'active' AND `+publicGameTeamSQL("gt")+`
		   AND gs.enabled = true AND (gs.release_at IS NULL OR gs.release_at <= NOW())
		 ORDER BY gt.is_nop, gt.team_number NULLS LAST, gt.name, gs.sort_order, gs.name`, view)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cell warCell
		if err := rows.Scan(&cell.TeamID, &cell.ServiceSlug, &cell.Status, &cell.BreachFlash); err != nil {
			rows.Close()
			return nil, err
		}
		room.Grid = append(room.Grid, cell)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := h.loadWarHills(ctx, room); err != nil {
		return nil, err
	}
	if err := h.loadWarEvents(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// loadWarHills shows every hill that is open or has a schedule; the holder is live
// because anyone can read it off the hill itself.
func (h *GameHandler) loadWarHills(ctx context.Context, room *warRoom) error {
	rows, err := h.db.Pool.Query(ctx,
		`WITH latest AS (
		   SELECT DISTINCT ON (hill_id) hill_id, controller_team_id
		   FROM game_koth_control ORDER BY hill_id, tick_number DESC
		 ), running AS (
		   SELECT round_number, planned_end FROM game_koth_rounds
		   WHERE status = 'running' ORDER BY round_number DESC LIMIT 1
		 )
		 SELECT h.slug, h.name, h.enabled, gt.name,
		        COALESCE((SELECT COUNT(*) FROM game_koth_control kc, running r
		                  WHERE kc.hill_id = h.id AND kc.round_number = r.round_number
		                    AND kc.controller_team_id = l.controller_team_id), 0),
		        COALESCE((SELECT GREATEST(0, EXTRACT(EPOCH FROM r.planned_end - NOW()))::int FROM running r), 0)
		 FROM game_koth_hills h
		 LEFT JOIN latest l ON l.hill_id = h.id
		 LEFT JOIN game_teams gt ON gt.id = l.controller_team_id AND `+publicGameTeamSQL("gt")+`
		 WHERE h.enabled = true OR EXISTS (SELECT 1 FROM game_koth_windows w WHERE w.hill_id = h.id)
		 ORDER BY h.sort_order, h.name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hl warHill
		if err := rows.Scan(&hl.Slug, &hl.Name, &hl.IsActive, &hl.HolderName, &hl.HeldTicks, &hl.RoundEndsIn); err != nil {
			return err
		}
		if hl.HolderName == nil {
			hl.HeldTicks = 0
		}
		if !hl.IsActive {
			hl.RoundEndsIn = 0
		}
		room.Hills = append(room.Hills, hl)
	}
	return rows.Err()
}

// loadWarEvents merges the feed: first blood per flag store and the first outbreak
// block per team and service (both capped at the view tick), crowns, advisories and
// hill windows.
func (h *GameHandler) loadWarEvents(ctx context.Context, room *warRoom) error {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT kind, team_name, service, hill, text, tick, at FROM (
		   SELECT 'first_blood' AS kind, a.name AS team_name, gs.name AS service, '' AS hill, '' AS text,
		          fb.tick_number AS tick, fb.submitted_at AS at
		   FROM (SELECT DISTINCT ON (cp.service_id, f.store_index)
		                cp.service_id, cp.attacker_team_id, cp.tick_number, cp.submitted_at
		         FROM game_captures cp JOIN game_flags f ON f.id = cp.flag_id
		         JOIN game_teams v ON v.id = cp.victim_team_id
		         WHERE NOT v.is_nop
		         ORDER BY cp.service_id, f.store_index, cp.submitted_at, cp.id) fb
		   JOIN game_teams a ON a.id = fb.attacker_team_id
		   JOIN game_services gs ON gs.id = fb.service_id
		   WHERE fb.tick_number <= $1 AND `+publicGameTeamSQL("a")+`
		 UNION ALL
		   SELECT 'shield_up', gt.name, gs.name, '', '', sb.tick, sb.at
		   FROM (SELECT DISTINCT ON (f.team_id, f.service_id) f.team_id, f.service_id, br.tick_number AS tick, br.created_at AS at
		         FROM game_bot_reads br JOIN game_flags f ON f.id = br.flag_id
		         WHERE br.outcome = 'blocked'
		         ORDER BY f.team_id, f.service_id, br.tick_number, br.created_at) sb
		   JOIN game_teams gt ON gt.id = sb.team_id
		   JOIN game_services gs ON gs.id = sb.service_id
		   WHERE sb.tick <= $1 AND NOT gt.is_nop AND `+publicGameTeamSQL("gt")+`
		 UNION ALL
		   SELECT ge.kind, COALESCE(gt.name, ''), COALESCE(gs.name, ''), COALESCE(kh.name, ''),
		          CASE WHEN ge.kind = 'advisory' THEN COALESCE(gs.advisories ->> ge.store_index::text, '') ELSE '' END,
		          ge.tick_number, ge.created_at
		   FROM game_events ge
		   LEFT JOIN game_teams gt ON gt.id = ge.team_id
		   LEFT JOIN game_services gs ON gs.id = ge.service_id
		   LEFT JOIN game_koth_hills kh ON kh.id = ge.hill_id
		   WHERE ge.kind IN ('crown', 'advisory', 'hill_open', 'hill_close')
		     AND ge.tick_number <= $1
		     AND (ge.team_id IS NULL OR `+publicGameTeamSQL("gt")+`)
		 ) feed
		 ORDER BY at DESC, tick DESC LIMIT $2`, room.ViewTick, warRoomEvents)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e warRoomEvent
		var at time.Time
		if err := rows.Scan(&e.Kind, &e.TeamName, &e.Service, &e.Hill, &e.Text, &e.Tick, &at); err != nil {
			return err
		}
		e.TS = at.UTC().Format(time.RFC3339)
		room.Events = append(room.Events, e)
	}
	return rows.Err()
}
