package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

type GameAdminHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewGameAdminHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *GameAdminHandler {
	return &GameAdminHandler{config: cfg, db: db, logger: logger}
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name, given string) string {
	if given != "" {
		return given
	}
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *GameAdminHandler) fail(c *gin.Context, where string, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		c.JSON(http.StatusConflict, gin.H{"error": "already exists"})
		return
	}
	h.logger.Error(where, zap.Error(err))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

// Teams

type createTeamReq struct {
	Name      string `json:"name" binding:"required"`
	Slug      string `json:"slug"`
	VulnboxIP string `json:"vulnbox_ip"`
	Token     string `json:"token"`
	IsNOP     bool   `json:"is_nop"`
}

func (h *GameAdminHandler) CreateTeam(c *gin.Context) {
	var req createTeamReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	var id uuid.UUID
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_teams (name, slug, vulnbox_ip, token, is_nop)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		req.Name, slugify(req.Name, req.Slug), nullIf(req.VulnboxIP), nullIf(req.Token), req.IsNOP).Scan(&id)
	if err != nil {
		h.fail(c, "create team", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *GameAdminHandler) ListTeams(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, vulnbox_ip::text, is_nop, status FROM game_teams ORDER BY name`)
	if err != nil {
		h.fail(c, "list teams", err)
		return
	}
	defer rows.Close()
	teams := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var name, slug, status string
		var ip *string
		var isNOP bool
		if rows.Scan(&id, &name, &slug, &ip, &isNOP, &status) != nil {
			continue
		}
		teams = append(teams, gin.H{"id": id, "name": name, "slug": slug, "vulnbox_ip": ip, "is_nop": isNOP, "status": status})
	}
	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

func (h *GameAdminHandler) DeleteTeam(c *gin.Context) {
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_teams WHERE id = $1`, c.Param("id")); err != nil {
		h.fail(c, "delete team", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

type addMemberReq struct {
	UserID string `json:"user_id" binding:"required"`
	Role   string `json:"role"`
}

func (h *GameAdminHandler) AddMember(c *gin.Context) {
	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	role := req.Role
	if role != "captain" {
		role = "member"
	}
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO game_team_members (team_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (team_id, user_id) DO UPDATE SET role = $3`,
		c.Param("id"), req.UserID, role); err != nil {
		h.fail(c, "add member", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "added"})
}

// Services

type createServiceReq struct {
	Name       string `json:"name" binding:"required"`
	Slug       string `json:"slug"`
	Category   string `json:"category" binding:"required"`
	Tier       string `json:"tier"`
	Port       int    `json:"port" binding:"required"`
	CheckerRef string `json:"checker_ref" binding:"required"`
	FlagStores int    `json:"flag_stores"`
}

func (h *GameAdminHandler) CreateService(c *gin.Context) {
	var req createServiceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, category, port, checker_ref required"})
		return
	}
	tier := req.Tier
	if tier != "stretch" {
		tier = "core"
	}
	stores := req.FlagStores
	if stores < 1 {
		stores = 1
	}
	var id uuid.UUID
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_services (name, slug, category, tier, port, checker_ref, flag_stores)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		req.Name, slugify(req.Name, req.Slug), req.Category, tier, req.Port, req.CheckerRef, stores).Scan(&id)
	if err != nil {
		h.fail(c, "create service", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *GameAdminHandler) ListServices(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, category, tier, port, checker_ref, enabled FROM game_services ORDER BY sort_order, name`)
	if err != nil {
		h.fail(c, "list services", err)
		return
	}
	defer rows.Close()
	services := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var name, slug, category, tier string
		var port *int
		var checker *string
		var enabled bool
		if rows.Scan(&id, &name, &slug, &category, &tier, &port, &checker, &enabled) != nil {
			continue
		}
		services = append(services, gin.H{"id": id, "name": name, "slug": slug, "category": category,
			"tier": tier, "port": port, "checker_ref": checker, "enabled": enabled})
	}
	c.JSON(http.StatusOK, gin.H{"services": services})
}

type enabledReq struct {
	Enabled *bool `json:"enabled"`
}

func (h *GameAdminHandler) UpdateService(c *gin.Context) {
	var req enabledReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
		return
	}
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_services SET enabled = $1, updated_at = NOW() WHERE id = $2`,
		*req.Enabled, c.Param("id")); err != nil {
		h.fail(c, "update service", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GameAdminHandler) DeleteService(c *gin.Context) {
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_services WHERE id = $1`, c.Param("id")); err != nil {
		h.fail(c, "delete service", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// Hills

type createHillReq struct {
	Name         string `json:"name" binding:"required"`
	Slug         string `json:"slug"`
	Host         string `json:"host" binding:"required"`
	Port         int    `json:"port" binding:"required"`
	CheckerRef   string `json:"checker_ref" binding:"required"`
	ResetSeconds int    `json:"reset_seconds"`
}

func (h *GameAdminHandler) CreateHill(c *gin.Context) {
	var req createHillReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, host, port, checker_ref required"})
		return
	}
	reset := req.ResetSeconds
	if reset < 1 {
		reset = 900
	}
	var id uuid.UUID
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_koth_hills (name, slug, host, port, checker_ref, reset_seconds)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		req.Name, slugify(req.Name, req.Slug), req.Host, req.Port, req.CheckerRef, reset).Scan(&id)
	if err != nil {
		h.fail(c, "create hill", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *GameAdminHandler) ListHills(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, host::text, port, checker_ref, reset_seconds, enabled
		 FROM game_koth_hills ORDER BY sort_order, name`)
	if err != nil {
		h.fail(c, "list hills", err)
		return
	}
	defer rows.Close()
	hills := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var name, slug string
		var host, checker *string
		var port, reset *int
		var enabled bool
		if rows.Scan(&id, &name, &slug, &host, &port, &checker, &reset, &enabled) != nil {
			continue
		}
		hills = append(hills, gin.H{"id": id, "name": name, "slug": slug, "host": host, "port": port,
			"checker_ref": checker, "reset_seconds": reset, "enabled": enabled})
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

func (h *GameAdminHandler) UpdateHill(c *gin.Context) {
	var req enabledReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
		return
	}
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_koth_hills SET enabled = $1 WHERE id = $2`,
		*req.Enabled, c.Param("id")); err != nil {
		h.fail(c, "update hill", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GameAdminHandler) DeleteHill(c *gin.Context) {
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_koth_hills WHERE id = $1`, c.Param("id")); err != nil {
		h.fail(c, "delete hill", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
