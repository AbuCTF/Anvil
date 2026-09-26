package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/instancer"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

type GameAdminHandler struct {
	config       *config.Config
	db           *database.DB
	instancerSvc *instancer.Service
	logger       *zap.Logger
}

func NewGameAdminHandler(cfg *config.Config, db *database.DB, instancerSvc *instancer.Service, logger *zap.Logger) *GameAdminHandler {
	return &GameAdminHandler{config: cfg, db: db, instancerSvc: instancerSvc, logger: logger}
}

// LaunchArena spawns the ONE shared contested instance for a shared-arena (KotH)
// challenge and registers/refreshes its hill: the engine then HTTP-polls the hill's
// /koth/status and drives /koth/reset with the injected KOTH_ADMIN_TOKEN. Idempotent
// per challenge (fixed "arena" owner => a deterministic single instance id); a
// re-launch refreshes the endpoint + secret and bumps the hill generation.
func (h *GameAdminHandler) LaunchArena(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	if h.instancerSvc == nil || !h.instancerSvc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "instancer backend is not available"})
		return
	}
	ctx := c.Request.Context()

	var name, slug, image, tag, cpu, mem string
	var portsJSON []byte
	err := h.db.Pool.QueryRow(ctx,
		`SELECT name, slug, COALESCE(container_image, ''), COALESCE(container_tag, 'latest'),
		        COALESCE(cpu_limit, '2'), COALESCE(memory_limit, '1024Mi'), COALESCE(exposed_ports, '[]'::jsonb)
		 FROM challenges
		 WHERE id = $1 AND arena_mode = 'shared' AND resource_type = 'docker'`,
		challengeID).Scan(&name, &slug, &image, &tag, &cpu, &mem, &portsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no shared-arena docker challenge with that id"})
		return
	}
	if err != nil {
		h.fail(c, "load arena challenge", err)
		return
	}
	if image == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge has no container_image"})
		return
	}

	secret, err := arenaResetSecret()
	if err != nil {
		h.fail(c, "generate reset secret", err)
		return
	}

	res, err := h.instancerSvc.Launch(ctx, instancer.LaunchSpec{
		TeamID:      "arena", // fixed owner => one shared instance per challenge (InstanceID = HMAC(arena, id))
		ChallengeID: challengeID,
		Slug:        slug,
		Image:       image,
		Tag:         tag,
		CPULimit:    cpu,
		MemoryLimit: mem,
		Ports:       []instancer.PortSpec{{Port: arenaHTTPPort(portsJSON), Protocol: "tcp", Service: "http"}},
		// KOTH_ADMIN_TOKEN: engine-only reset/status secret. KOTH_VERIFY_URL + KOTH_REQUIRE_SECRET:
		// the write-gate - the target POSTs {token, rpc_secret} here to authorize each write, so a
		// public token from /koth/status can't act for another team. Challenge id is baked into the
		// URL so a token from another arena won't verify. Injected for every arena; targets that
		// don't use the gate (e.g. GridWatch) simply ignore these vars.
		Flags: map[string]string{
			"KOTH_ADMIN_TOKEN":    secret,
			"KOTH_VERIFY_URL":     fmt.Sprintf("http://anvil-api.anvil.svc.cluster.local:8080/api/v1/arena/koth/%s/verify", challengeID),
			"KOTH_REQUIRE_SECRET": "true",
		},
		Timeout: 720 * time.Hour, // long-lived: the arena runs the whole event
	})
	if err != nil {
		h.fail(c, "launch arena instance", err)
		return
	}

	baseURL := arenaBaseURL(res.Endpoints)
	if baseURL == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "instance launched but no endpoint was published"})
		return
	}

	if _, err := h.db.Pool.Exec(ctx,
		`INSERT INTO game_koth_hills (name, slug, challenge_id, base_url, reset_secret, enabled, generation)
		 VALUES ($1, $2, $3, $4, $5, TRUE, 0)
		 ON CONFLICT (slug) DO UPDATE SET
		   name = EXCLUDED.name, challenge_id = EXCLUDED.challenge_id,
		   base_url = EXCLUDED.base_url, reset_secret = EXCLUDED.reset_secret,
		   enabled = TRUE, generation = game_koth_hills.generation + 1`,
		name, slug, challengeID, baseURL, secret); err != nil {
		h.fail(c, "register arena hill", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "launched", "slug": slug, "instance_id": res.InstanceID, "base_url": baseURL,
		"message": "shared arena is up; enable the game engine to start scoring holds",
	})
}

// StopArena tears down the shared instance and disables its hill.
func (h *GameAdminHandler) StopArena(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	ctx := c.Request.Context()
	if h.instancerSvc != nil && h.instancerSvc.Enabled() {
		if err := h.instancerSvc.Destroy(ctx, h.instancerSvc.InstanceID("arena", challengeID)); err != nil {
			h.logger.Warn("stop arena: destroy instance", zap.Error(err))
		}
	}
	if _, err := h.db.Pool.Exec(ctx,
		`UPDATE game_koth_hills SET enabled = FALSE WHERE challenge_id = $1`, challengeID); err != nil {
		h.fail(c, "disable arena hill", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// arenaResetSecret is the engine-only KOTH_ADMIN_TOKEN injected into the target's env.
func arenaResetSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// arenaHTTPPort reads the first declared exposed port, defaulting to 8080 (the KotH convention).
func arenaHTTPPort(portsJSON []byte) int {
	var ports []struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(portsJSON, &ports); err == nil {
		for _, p := range ports {
			if p.Port > 0 {
				return p.Port
			}
		}
	}
	return 8080
}

// arenaBaseURL picks an engine-reachable base URL from the published endpoints:
// a URL-shaped connect string if present, else https://<host>.
func arenaBaseURL(eps []instancer.Endpoint) string {
	for _, ep := range eps {
		if strings.Contains(ep.Connect, "://") {
			return strings.TrimRight(ep.Connect, "/")
		}
	}
	for _, ep := range eps {
		if ep.Host != "" {
			return "https://" + ep.Host
		}
	}
	return ""
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
