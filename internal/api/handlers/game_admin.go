package handlers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

const maximumArenaAdminRequest = int64(64 << 10)

func slugify(name, given string) string {
	source := strings.TrimSpace(given)
	if source == "" {
		source = strings.TrimSpace(name)
	}
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(source), "-"), "-")
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func bindArenaAdminJSON(c *gin.Context, destination any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumArenaAdminRequest)
	return c.ShouldBindJSON(destination)
}

func (h *GameAdminHandler) fail(c *gin.Context, where string, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			c.JSON(http.StatusConflict, gin.H{"error": "already exists"})
			return
		case "23503":
			c.JSON(http.StatusBadRequest, gin.H{"error": "referenced resource not found"})
			return
		case "22001", "22P02", "23514":
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
	}
	h.logger.Error(where, zap.Error(err))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

func validatedArenaID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return uuid.Nil, false
	}
	return id, true
}

func validateArenaText(value, field string, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > maximum {
		return "", fmt.Errorf("%s must be at most %d characters", field, maximum)
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("%s contains control characters", field)
	}
	return value, nil
}

func validateArenaSlug(name, given string) (string, error) {
	slug := slugify(name, given)
	if slug == "" {
		return "", errors.New("slug must contain a letter or number")
	}
	if len(slug) > 100 {
		return "", errors.New("slug must be at most 100 characters")
	}
	return slug, nil
}

func (h *GameAdminHandler) requireAffected(c *gin.Context, where string, affected int64) bool {
	if affected == 1 {
		return true
	}
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return false
	}
	h.logger.Error(where, zap.Int64("rows_affected", affected))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	return false
}

// teams

type createTeamReq struct {
	Name      string `json:"name" binding:"required"`
	Slug      string `json:"slug"`
	VulnboxIP string `json:"vulnbox_ip"`
	Token     string `json:"token"`
	IsNOP     bool   `json:"is_nop"`
}

func (h *GameAdminHandler) CreateTeam(c *gin.Context) {
	var req createTeamReq
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	name, err := validateArenaText(req.Name, "name", 100)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slug, err := validateArenaSlug(name, req.Slug)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ip := strings.TrimSpace(req.VulnboxIP)
	if ip != "" {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vulnbox_ip must be a valid IP address"})
			return
		}
		ip = parsed.String()
	}
	token := strings.TrimSpace(req.Token)
	if len(token) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token must be at most 64 characters"})
		return
	}
	if strings.IndexFunc(token, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token contains invalid characters"})
		return
	}
	var id uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_teams (name, slug, vulnbox_ip, token, is_nop)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		name, slug, nullIf(ip), nullIf(token), req.IsNOP).Scan(&id)
	if err != nil {
		h.fail(c, "create team", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *GameAdminHandler) ListTeams(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, host(vulnbox_ip), is_nop, status FROM game_teams ORDER BY name`)
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
		if err := rows.Scan(&id, &name, &slug, &ip, &isNOP, &status); err != nil {
			h.fail(c, "scan team", err)
			return
		}
		teams = append(teams, gin.H{"id": id, "name": name, "slug": slug, "vulnbox_ip": ip, "is_nop": isNOP, "status": status})
	}
	if err := rows.Err(); err != nil {
		h.fail(c, "iterate teams", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

func (h *GameAdminHandler) DeleteTeam(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_teams WHERE id = $1`, id)
	if err != nil {
		h.fail(c, "delete team", err)
		return
	}
	if !h.requireAffected(c, "delete team affected unexpected rows", result.RowsAffected()) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

type addMemberReq struct {
	UserID string `json:"user_id" binding:"required"`
	Role   string `json:"role"`
}

func (h *GameAdminHandler) AddMember(c *gin.Context) {
	teamID, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	var req addMemberReq
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "member"
	}
	if role != "member" && role != "captain" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be member or captain"})
		return
	}
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.fail(c, "begin add member", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"anvil-game-team-member:"+userID.String()); err != nil {
		h.fail(c, "lock team member", err)
		return
	}
	var existingTeamID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT team_id FROM game_team_members WHERE user_id = $1 AND team_id <> $2 LIMIT 1`,
		userID, teamID).Scan(&existingTeamID)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "user is already assigned to another team",
			"team_id": existingTeamID,
		})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.fail(c, "check existing team membership", err)
		return
	}
	result, err := tx.Exec(ctx,
		`INSERT INTO game_team_members (team_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (team_id, user_id) DO UPDATE SET role = $3`,
		teamID, userID, role)
	if err != nil {
		h.fail(c, "add member", err)
		return
	}
	if !h.requireAffected(c, "add member affected unexpected rows", result.RowsAffected()) {
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.fail(c, "commit add member", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "added"})
}

// services

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
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, category, port, checker_ref required"})
		return
	}
	name, err := validateArenaText(req.Name, "name", 100)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slug, err := validateArenaSlug(name, req.Slug)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	category := strings.ToLower(strings.TrimSpace(req.Category))
	if !validArenaCategory(category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported category"})
		return
	}
	tier := strings.ToLower(strings.TrimSpace(req.Tier))
	if tier == "" {
		tier = "core"
	}
	if tier != "core" && tier != "stretch" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tier must be core or stretch"})
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
		return
	}
	checkerRef, err := validateArenaText(req.CheckerRef, "checker_ref", 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	stores := req.FlagStores
	if stores == 0 {
		stores = 1
	}
	if stores != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only one flag store is currently supported"})
		return
	}
	var id uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_services (name, slug, category, tier, port, checker_ref, flag_stores)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		name, slug, category, tier, req.Port, checkerRef, stores).Scan(&id)
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
		if err := rows.Scan(&id, &name, &slug, &category, &tier, &port, &checker, &enabled); err != nil {
			h.fail(c, "scan service", err)
			return
		}
		services = append(services, gin.H{"id": id, "name": name, "slug": slug, "category": category,
			"tier": tier, "port": port, "checker_ref": checker, "enabled": enabled})
	}
	if err := rows.Err(); err != nil {
		h.fail(c, "iterate services", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": services})
}

func validArenaCategory(category string) bool {
	switch category {
	case "pwn", "web", "crypto", "misc", "rev", "forensics":
		return true
	default:
		return false
	}
}

type enabledReq struct {
	Enabled *bool `json:"enabled"`
}

func (h *GameAdminHandler) UpdateService(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	var req enabledReq
	if err := bindArenaAdminJSON(c, &req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_services SET enabled = $1, updated_at = NOW() WHERE id = $2`,
		*req.Enabled, id)
	if err != nil {
		h.fail(c, "update service", err)
		return
	}
	if !h.requireAffected(c, "update service affected unexpected rows", result.RowsAffected()) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GameAdminHandler) DeleteService(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_services WHERE id = $1`, id)
	if err != nil {
		h.fail(c, "delete service", err)
		return
	}
	if !h.requireAffected(c, "delete service affected unexpected rows", result.RowsAffected()) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// hills

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
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, host, port, checker_ref required"})
		return
	}
	name, err := validateArenaText(req.Name, "name", 100)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slug, err := validateArenaSlug(name, req.Slug)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	host := strings.TrimSpace(req.Host)
	parsedHost := net.ParseIP(host)
	if parsedHost == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host must be a valid IP address"})
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
		return
	}
	checkerRef, err := validateArenaText(req.CheckerRef, "checker_ref", 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reset := req.ResetSeconds
	if reset == 0 {
		reset = 900
	}
	if reset < 1 || reset > 24*60*60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reset_seconds must be between 1 and 86400"})
		return
	}
	var id uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_koth_hills (name, slug, host, port, checker_ref, reset_seconds)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		name, slug, parsedHost.String(), req.Port, checkerRef, reset).Scan(&id)
	if err != nil {
		h.fail(c, "create hill", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *GameAdminHandler) ListHills(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, host(host), port, checker_ref, reset_seconds, enabled
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
		if err := rows.Scan(&id, &name, &slug, &host, &port, &checker, &reset, &enabled); err != nil {
			h.fail(c, "scan hill", err)
			return
		}
		hills = append(hills, gin.H{"id": id, "name": name, "slug": slug, "host": host, "port": port,
			"checker_ref": checker, "reset_seconds": reset, "enabled": enabled})
	}
	if err := rows.Err(); err != nil {
		h.fail(c, "iterate hills", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

func (h *GameAdminHandler) UpdateHill(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	var req enabledReq
	if err := bindArenaAdminJSON(c, &req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_koth_hills SET enabled = $1 WHERE id = $2`,
		*req.Enabled, id)
	if err != nil {
		h.fail(c, "update hill", err)
		return
	}
	if !h.requireAffected(c, "update hill affected unexpected rows", result.RowsAffected()) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GameAdminHandler) DeleteHill(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM game_koth_hills WHERE id = $1`, id)
	if err != nil {
		h.fail(c, "delete hill", err)
		return
	}
	if !h.requireAffected(c, "delete hill affected unexpected rows", result.RowsAffected()) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
