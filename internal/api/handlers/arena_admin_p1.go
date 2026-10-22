package handlers

import (
	"net/http"
	"strconv"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ArenaAdminHandler handles admin endpoints added by the P1-TeamAPI build session:
// team token rotation, vulnbox IP editing, private checker log, suspicious SLA report.
type ArenaAdminHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewArenaAdminHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *ArenaAdminHandler {
	return &ArenaAdminHandler{config: cfg, db: db, logger: logger}
}

// RotateTeamToken generates (or rotates) the submit token for a game team.
// POST /api/v1/admin/arena/teams/:id/token
func (h *ArenaAdminHandler) RotateTeamToken(c *gin.Context) {
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	tok, err := game.RotateTeamToken(c.Request.Context(), h.db, teamID)
	if err != nil {
		h.logger.Error("admin: rotate team token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok})
}

// UpdateVulnboxIP sets the vulnbox_ip for a game team.
// PATCH /api/v1/admin/arena/teams/:id/vulnbox-ip
func (h *ArenaAdminHandler) UpdateVulnboxIP(c *gin.Context) {
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	var body struct {
		VulnboxIP string `json:"vulnbox_ip" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vulnbox_ip required"})
		return
	}
	tag, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_teams SET vulnbox_ip = $1 WHERE id = $2`, body.VulnboxIP, teamID)
	if err != nil {
		h.logger.Error("admin: update vulnbox ip", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type checkerLogRow struct {
	ID          int64   `json:"id"`
	TickNumber  int     `json:"tick_number"`
	RelatedTick int     `json:"related_tick"`
	TeamID      string  `json:"team_id"`
	ServiceID   string  `json:"service_id"`
	Method      string  `json:"method"`
	Variant     int     `json:"variant"`
	Status      *string `json:"status"`
	Code        *string `json:"code"`
	Message     *string `json:"message"`
	LatencyMS   *int    `json:"latency_ms"`
}

// CheckerLog returns checker task rows for a (team, service, tick) triple.
// GET /api/v1/admin/arena/checker-log?team_id=&service_id=&tick=
func (h *ArenaAdminHandler) CheckerLog(c *gin.Context) {
	teamID, err := uuid.Parse(c.Query("team_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_id required"})
		return
	}
	serviceID, err := uuid.Parse(c.Query("service_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service_id required"})
		return
	}
	tick, err := strconv.Atoi(c.Query("tick"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tick required"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, tick_number, related_tick, team_id, service_id, method, variant,
		        status, code, message, latency_ms
		 FROM game_checker_tasks
		 WHERE team_id = $1 AND service_id = $2 AND tick_number = $3
		 ORDER BY id`, teamID, serviceID, tick)
	if err != nil {
		h.logger.Error("admin: checker log query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	var result []checkerLogRow
	for rows.Next() {
		var r checkerLogRow
		if err := rows.Scan(
			&r.ID, &r.TickNumber, &r.RelatedTick,
			&r.TeamID, &r.ServiceID,
			&r.Method, &r.Variant,
			&r.Status, &r.Code,
			&r.Message, &r.LatencyMS,
		); err != nil {
			h.logger.Error("admin: checker log scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if r.Status != nil {
			var code, message string
			if r.Code != nil {
				code = *r.Code
			}
			if r.Message != nil {
				message = *r.Message
			}
			code, message = game.SanitizeCheckerDiagnostic(models.SLAStatus(*r.Status), code, message)
			if code == "" {
				r.Code = nil
			} else {
				r.Code = &code
			}
			r.Message = &message
		}
		result = append(result, r)
	}
	c.JSON(http.StatusOK, gin.H{"tasks": result})
}

type suspiciousSLARow struct {
	TeamID    string  `json:"team_id"`
	TeamName  string  `json:"team_name"`
	ServiceID string  `json:"service_id"`
	SvcName   string  `json:"service_name"`
	Tick      int     `json:"tick"`
	DownCount int     `json:"down_count"`
	TotalTick int     `json:"total_in_tick"`
	DownRate  float64 `json:"down_rate"`
}

// --- pcap and vulnbox control stubs (M8) ---

// PcapCapture is a stub endpoint for triggering a pcap capture on a vulnbox.
// POST /api/v1/arena/teams/:id/pcap
// Returns 501 Not Implemented until the capture backend is wired.
func (h *ArenaAdminHandler) PcapCapture(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "pcap capture not yet implemented",
		"hint":  "will accept: {duration_sec, filter} and return a signed GCS URL",
	})
}

// VulnboxReset is a stub endpoint for requesting a soft-reset of a vulnbox service.
// POST /api/v1/arena/teams/:id/vulnbox-reset
// Returns 501 Not Implemented until the OCI/infra control plane is wired.
func (h *ArenaAdminHandler) VulnboxReset(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "vulnbox reset not yet implemented",
		"hint":  "will accept: {service_id?} and trigger a docker-compose restart on the vulnbox",
	})
}

// VulnboxReboot is a stub endpoint for rebooting the entire vulnbox VM.
// POST /api/v1/arena/teams/:id/vulnbox-reboot
// Returns 501 Not Implemented until the OCI control plane is wired.
func (h *ArenaAdminHandler) VulnboxReboot(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "vulnbox reboot not yet implemented",
		"hint":  "will call OCI instance action=RESET for the team's VM",
	})
}

// SuspiciousSLA returns (team, service, tick) triples where checker task DOWN+FAULTY
// rate exceeds 60% with at least 3 tasks. Useful for detecting checker bugs or
// organised defence bypasses.
// GET /api/v1/admin/arena/sla-suspicious?since_tick=&limit=
func (h *ArenaAdminHandler) SuspiciousSLA(c *gin.Context) {
	sinceTick := 0
	if v := c.Query("since_tick"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			sinceTick = n
		}
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT t.id::text, t.name, s.id::text, s.name, ct.tick_number,
		        SUM(CASE WHEN ct.status IN ('DOWN','FAULTY') THEN 1 ELSE 0 END) AS down_count,
		        COUNT(*) AS total_in_tick,
		        ROUND(SUM(CASE WHEN ct.status IN ('DOWN','FAULTY') THEN 1 ELSE 0 END)::numeric / COUNT(*), 3) AS down_rate
		 FROM game_checker_tasks ct
		 JOIN game_teams t ON t.id = ct.team_id
		 JOIN game_services s ON s.id = ct.service_id
		 WHERE ct.tick_number >= $1 AND ct.method <> 'exploit'
		 GROUP BY t.id, t.name, s.id, s.name, ct.tick_number
		 HAVING COUNT(*) >= 3
		    AND SUM(CASE WHEN ct.status IN ('DOWN','FAULTY') THEN 1 ELSE 0 END)::numeric / COUNT(*) >= 0.6
		 ORDER BY ct.tick_number DESC, down_rate DESC
		 LIMIT $2`, sinceTick, limit)
	if err != nil {
		h.logger.Error("admin: suspicious sla query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	var result []suspiciousSLARow
	for rows.Next() {
		var r suspiciousSLARow
		if err := rows.Scan(
			&r.TeamID, &r.TeamName, &r.ServiceID, &r.SvcName,
			&r.Tick, &r.DownCount, &r.TotalTick, &r.DownRate,
		); err != nil {
			h.logger.Error("admin: suspicious sla scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		result = append(result, r)
	}
	c.JSON(http.StatusOK, gin.H{"rows": result})
}
