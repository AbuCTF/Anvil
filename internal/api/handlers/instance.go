package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/instancer"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

type InstanceService struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	logger       *zap.Logger
}

func NewInstanceService(cfg *config.Config, db *database.DB, containerSvc *container.Service, logger *zap.Logger) *InstanceService {
	return &InstanceService{
		config:       cfg,
		db:           db,
		containerSvc: containerSvc,
		logger:       logger,
	}
}

type InstanceResponse struct {
	ID             string         `json:"id"`
	ChallengeID    string         `json:"challenge_id"`
	ChallengeName  string         `json:"challenge_name"`
	ChallengeSlug  string         `json:"challenge_slug"`
	ContainerID    string         `json:"container_id,omitempty"`
	Status         string         `json:"status"`
	IPAddress      string         `json:"ip_address,omitempty"`
	Ports          map[string]int `json:"ports,omitempty"`
	CreatedAt      int64          `json:"created_at"`
	ExpiresAt      int64          `json:"expires_at"`
	ExtensionsUsed int            `json:"extensions_used"`
	MaxExtensions  int            `json:"max_extensions"`
	ResetCount     int            `json:"reset_count"`
	MaxResets      int            `json:"max_resets"`
	// Endpoints is set for k8s-instanced challenges: the player-facing URLs /
	// connect strings. IPAddress/Ports stay empty in that mode.
	Endpoints []instancer.Endpoint `json:"endpoints,omitempty"`
}

type CreateInstanceRequest struct {
	ChallengeSlug string `json:"challenge_slug" binding:"required"`
}

type instanceChallenge struct {
	ID                string
	Name              string
	Slug              string
	ResourceType      string
	ContainerImage    string
	ContainerTag      string
	ContainerPlatform string
	CPULimit          string
	MemoryLimit       string
	ExposedPorts      []byte
	ContainerSpec     []byte // multi-container roles; NULL/empty => single-image
	InstanceTimeout   *int
	MaxExtensions     *int
	MaxResets         int
	Privesc           bool // relax securityContext (allowPrivilegeEscalation:true) for boot-to-root/SUID challenges
}

type instancePortConfig struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Service  string `json:"service"`
}

// serviceConfig mirrors the admin ContainerService JSON stored in container_spec.
type serviceConfig struct {
	Name        string               `json:"name"`
	Image       string               `json:"image"`
	Tag         string               `json:"tag"`
	Command     []string             `json:"command"`
	Public      bool                 `json:"public"`
	Egress      bool                 `json:"egress"`
	Privesc     bool                 `json:"privesc"`
	Ports       []instancePortConfig `json:"ports"`
	Env         map[string]string    `json:"env"`
	CPULimit    string               `json:"cpu_limit"`
	MemoryLimit string               `json:"memory_limit"`
}

type instanceProvisionPlan struct {
	challenge  instanceChallenge
	portConfig []instancePortConfig
	services   []serviceConfig
	vmTemplate *vm.VMTemplate
}

type instanceOperationError struct {
	status int
	body   gin.H
	cause  error
}

func newInstanceOperationError(status int, message string, cause error) *instanceOperationError {
	return &instanceOperationError{status: status, body: gin.H{"error": message}, cause: cause}
}

type instanceRuntime struct {
	ID               uuid.UUID
	RuntimeID        *string
	Status           string
	ChallengeID      string
	ResourceType     string
	VMNodeID         *uuid.UUID
	ReservedVCPU     int
	ReservedMemoryMB int
}

// scopes instance ownership for reads: team_id in teams mode when the caller is
// on a team, else user_id (a teamless caller owns no team instances).
func (h *InstanceHandler) instanceOwnerScope(ctx context.Context, uid uuid.UUID) (string, interface{}, error) {
	teamsMode, err := isTeamsMode(ctx, h.db)
	if err != nil {
		return "", nil, err
	}
	if teamsMode {
		teamID, err := resolveTeamID(ctx, h.db, uid)
		if err != nil {
			return "", nil, err
		}
		if teamID != nil {
			return "team_id", *teamID, nil
		}
	}
	return "user_id", uid, nil
}

func (h *InstanceHandler) List(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx := c.Request.Context()
	// teams mode: any member sees the whole team's instances; off => per-user
	ownerCol, ownerArg, err := h.instanceOwnerScope(ctx, uid)
	if err != nil {
		h.logger.Error("failed to resolve instance owner scope", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}

	query := fmt.Sprintf(`
		SELECT
			i.id, i.challenge_id, i.container_id, i.status,
			i.ip_address, i.assigned_ports, i.created_at, i.expires_at,
			i.extensions_used, COALESCE(c.max_extensions, 3) as max_extensions,
			COALESCE(i.reset_count, 0), COALESCE(i.max_resets, c.max_resets, 3),
			c.name as challenge_name, c.slug as challenge_slug
		FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.%s = $1
		  AND i.status NOT IN ('stopped', 'failed', 'expired')
		  AND i.expires_at > NOW()
		ORDER BY i.created_at DESC
	`, ownerCol)

	rows, err := h.db.Pool.Query(ctx, query, ownerArg)
	if err != nil {
		h.logger.Error("failed to list instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}

	defer rows.Close()

	var instances []InstanceResponse
	for rows.Next() {
		var inst InstanceResponse
		var portsJSON []byte
		var createdAt, expiresAt time.Time
		var ipAddress, containerID *string

		if err := rows.Scan(
			&inst.ID, &inst.ChallengeID, &containerID, &inst.Status,
			&ipAddress, &portsJSON, &createdAt, &expiresAt,
			&inst.ExtensionsUsed, &inst.MaxExtensions,
			&inst.ResetCount, &inst.MaxResets,
			&inst.ChallengeName, &inst.ChallengeSlug,
		); err != nil {
			h.logger.Error("failed to scan instance", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
			return
		}

		if containerID != nil {
			inst.ContainerID = *containerID
		}
		if ipAddress != nil {
			inst.IPAddress = *ipAddress
		}
		inst.CreatedAt = createdAt.Unix()
		inst.ExpiresAt = expiresAt.Unix()

		if len(portsJSON) > 0 {
			if err := json.Unmarshal(portsJSON, &inst.Ports); err != nil {
				h.logger.Error("failed to parse assigned_ports", zap.String("instance_id", inst.ID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
				return
			}
		}
		if inst.Ports == nil {
			inst.Ports = make(map[string]int)
		}

		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}

	if instances == nil {
		instances = []InstanceResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"total":     len(instances),
	})
}

func (h *InstanceHandler) loadPublishedChallenge(
	ctx context.Context,
	querier instanceRowQuerier,
	slug string,
	staff bool,
) (instanceChallenge, error) {
	// staff (admin/author) can launch draft challenges to preview them pre-release.
	statusCond := "status = 'published' AND (release_date IS NULL OR release_date <= NOW())"
	if staff {
		statusCond = "status IN ('published', 'draft')"
	}
	var challenge instanceChallenge
	err := querier.QueryRow(ctx,
		`SELECT id, name, slug, resource_type, COALESCE(container_image, ''),
		        COALESCE(container_tag, 'latest'), COALESCE(container_platform, ''),
		        COALESCE(cpu_limit, '1'), COALESCE(memory_limit, '512Mi'),
		        COALESCE(exposed_ports, '[]'::jsonb), instance_timeout,
		        max_extensions, COALESCE(max_resets, 3),
		        COALESCE(container_spec, 'null'::jsonb), COALESCE(privesc, false)
		 FROM challenges
		 WHERE slug = $1 AND `+statusCond, slug).Scan(
		&challenge.ID, &challenge.Name, &challenge.Slug, &challenge.ResourceType,
		&challenge.ContainerImage, &challenge.ContainerTag, &challenge.ContainerPlatform,
		&challenge.CPULimit, &challenge.MemoryLimit, &challenge.ExposedPorts,
		&challenge.InstanceTimeout, &challenge.MaxExtensions, &challenge.MaxResets,
		&challenge.ContainerSpec, &challenge.Privesc,
	)
	return challenge, err
}

func (h *InstanceHandler) checkVPNEligibility(
	ctx context.Context,
	querier instanceRowQuerier,
	uid uuid.UUID,
) *instanceOperationError {
	if h.config == nil || !h.config.VPN.Enabled {
		return nil
	}
	requireVPN, err := h.boolSetting(ctx, querier, "platform.require_vpn", true)
	if err != nil {
		return newInstanceOperationError(http.StatusInternalServerError, "failed to check instance eligibility", err)
	}
	if !requireVPN {
		return nil
	}

	var lastHandshake *time.Time
	err = querier.QueryRow(ctx,
		`SELECT last_handshake FROM vpn_configs WHERE user_id = $1`, uid,
	).Scan(&lastHandshake)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return newInstanceOperationError(http.StatusInternalServerError, "failed to check VPN connection", err)
	}
	onlineWindow := h.config.VPN.OnlineWindow
	if onlineWindow <= 0 {
		onlineWindow = 45 * time.Second
	}
	if lastHandshake == nil || time.Since(*lastHandshake) >= onlineWindow {
		return newInstanceOperationError(http.StatusForbidden, "an active VPN connection is required to start an instance", nil)
	}
	return nil
}

func (h *InstanceHandler) prepareProvisionPlan(
	ctx context.Context,
	querier instanceRowQuerier,
	challenge instanceChallenge,
) (instanceProvisionPlan, *instanceOperationError) {
	plan := instanceProvisionPlan{challenge: challenge}
	if challenge.MaxResets < 0 {
		return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has an invalid reset limit", nil)
	}
	switch challenge.ResourceType {
	case "docker":
		if h.usingK8s() {
			if !h.instancerSvc.Enabled() {
				return plan, newInstanceOperationError(http.StatusServiceUnavailable, "instancer service is not configured on this server", nil)
			}
		} else if h.containerSvc == nil {
			return plan, newInstanceOperationError(http.StatusServiceUnavailable, "container service is not configured on this server", nil)
		}
		// multi-container challenges carry their images per-role in container_spec,
		// so an empty top-level container_image is fine when a spec is present.
		hasContainerSpec := len(challenge.ContainerSpec) > 0 && string(challenge.ContainerSpec) != "null"
		if challenge.ContainerImage == "" && !hasContainerSpec {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "no container image configured for this challenge", nil)
		}
		if err := json.Unmarshal(challenge.ExposedPorts, &plan.portConfig); err != nil {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has invalid port configuration", err)
		}
		for _, port := range plan.portConfig {
			if port.Port < 1 || port.Port > 65535 {
				return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has invalid port configuration", fmt.Errorf("port %d is out of range", port.Port))
			}
		}
		// optional multi-container roles (compose-style); absent => single image
		if hasContainerSpec {
			if err := json.Unmarshal(challenge.ContainerSpec, &plan.services); err != nil {
				return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has invalid container spec", err)
			}
			publicCount := 0
			for _, svc := range plan.services {
				if svc.Name == "" {
					return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge container spec has an unnamed service", nil)
				}
				if svc.Public {
					publicCount++
				}
				for _, port := range svc.Ports {
					if port.Port < 1 || port.Port > 65535 {
						return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has invalid port configuration", fmt.Errorf("service %s port %d out of range", svc.Name, port.Port))
					}
				}
			}
			if len(plan.services) > 0 && publicCount == 0 {
				return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge container spec has no public service", nil)
			}
		}
	case "vm":
		if h.vmSvc == nil {
			err := newInstanceOperationError(http.StatusServiceUnavailable, "VM service is not configured on this server", nil)
			err.body["hint"] = "This challenge requires VM infrastructure which is not configured"
			return plan, err
		}
		var templateID string
		err := querier.QueryRow(ctx,
			`SELECT vm_template_id FROM challenge_resources
			 WHERE challenge_id = $1 AND resource_type = 'vm' AND is_active = TRUE
			 ORDER BY sort_order LIMIT 1`, challenge.ID).Scan(&templateID)
		if errors.Is(err, pgx.ErrNoRows) {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "VM template not configured for this challenge", err)
		}
		if err != nil {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "failed to load VM template", err)
		}
		var template vm.VMTemplate
		err = querier.QueryRow(ctx,
			`SELECT id, name, COALESCE(description, ''), image_path, original_format,
			        COALESCE(image_size, 0), vcpu, memory_mb, COALESCE(disk_gb, 0),
			        COALESCE(os_type, 'linux'), created_at, updated_at
			 FROM vm_templates WHERE id = $1`, templateID).Scan(
			&template.ID, &template.Name, &template.Description,
			&template.ImagePath, &template.ImageFormat, &template.ImageSize,
			&template.VCPU, &template.MemoryMB, &template.DiskGB,
			&template.OS, &template.CreatedAt, &template.UpdatedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "VM template not found", err)
		}
		if err != nil {
			return plan, newInstanceOperationError(http.StatusInternalServerError, "failed to load VM template", err)
		}
		plan.vmTemplate = &template
	default:
		return plan, newInstanceOperationError(http.StatusInternalServerError, "challenge has an unsupported resource type", fmt.Errorf("unsupported resource type %q", challenge.ResourceType))
	}
	return plan, nil
}

func (h *InstanceHandler) instanceTimeout(challenge instanceChallenge) time.Duration {
	timeout := time.Hour
	if h.config != nil && h.config.Container.DefaultTimeout > 0 {
		timeout = h.config.Container.DefaultTimeout
	}
	if challenge.InstanceTimeout != nil && *challenge.InstanceTimeout > 0 {
		timeout = time.Duration(*challenge.InstanceTimeout) * time.Minute
	}
	return timeout
}

func (h *InstanceHandler) writeInstanceOperationError(c *gin.Context, logMessage string, opErr *instanceOperationError) {
	if opErr.cause != nil {
		h.logger.Error(logMessage, zap.Error(opErr.cause))
	}
	c.JSON(opErr.status, opErr.body)
}

func (h *InstanceHandler) Create(c *gin.Context) {
	// no launching challenge instances before the CTF starts (staff excepted, for testing).
	instPhase, instStaff := eventPlayState(c, h.db)
	if !instStaff && instPhase == "scheduled" {
		c.JSON(http.StatusForbidden, gin.H{"error": "the competition hasn't started yet"})
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// admins test freely: they skip the team requirement, economy gate, concurrency
	// limit, and cooldown so any challenge can be previewed without setup.
	isAdmin := c.GetString("role") == "admin"

	// settings + team membership are read before the tx: a pooled read while a
	// tx holds its own connection deadlocks the pool once a launch wave fills it.
	teamsMode, err := isTeamsMode(ctx, h.db)
	if err != nil {
		h.logger.Error("failed to read teams_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}
	var teamID *uuid.UUID
	if teamsMode && !isAdmin {
		if teamID, err = resolveTeamID(ctx, h.db, uid); err != nil {
			h.logger.Error("failed to resolve team", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
			return
		}
		if teamID == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "join a team before starting an instance"})
			return
		}
	}
	economyOn := false
	if teamID != nil {
		if economyOn, err = isEconomyMode(ctx, h.db); err != nil {
			h.logger.Error("failed to read economy_mode", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
			return
		}
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin instance admission transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// serialize admission per user across all api processes: closes the
	// count-then-insert and same-challenge races without holding a tx open
	// while docker or libvirt does slow external work.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"anvil-instance-create:"+uid.String()); err != nil {
		h.logger.Error("failed to acquire instance admission lock", zap.Error(err), zap.String("user_id", uid.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance eligibility"})
		return
	}

	challenge, err := h.loadPublishedChallenge(ctx, tx, req.ChallengeSlug, instStaff)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load challenge for instance creation", zap.Error(err), zap.String("slug", req.ChallengeSlug))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load challenge"})
		return
	}

	// static/download-only challenges have nothing to spawn; reject cleanly
	// instead of 500ing from the provision plan. multi-container challenges carry
	// their images in container_spec, so a non-null spec is instanceable too.
	hasContainerSpec := len(challenge.ContainerSpec) > 0 && string(challenge.ContainerSpec) != "null"
	if challenge.ResourceType == "docker" && challenge.ContainerImage == "" && !hasContainerSpec {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this challenge has no instance to start"})
		return
	}

	if opErr := h.checkVPNEligibility(ctx, tx, uid); opErr != nil {
		h.writeInstanceOperationError(c, "failed to check instance eligibility", opErr)
		return
	}

	plan, opErr := h.prepareProvisionPlan(ctx, tx, challenge)
	if opErr != nil {
		h.writeInstanceOperationError(c, "failed to prepare instance", opErr)
		return
	}

	var cooldownUntil time.Time
	err = tx.QueryRow(ctx,
		`SELECT cooldown_until FROM user_cooldowns WHERE user_id = $1 AND challenge_id = $2`,
		uid, challenge.ID).Scan(&cooldownUntil)
	if err == nil && time.Now().Before(cooldownUntil) && !isAdmin {
		remainingSeconds := max(0, int(time.Until(cooldownUntil).Seconds()))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":             "cooldown period active",
			"cooldown_until":    cooldownUntil.Unix(),
			"remaining_seconds": remainingSeconds,
			"message":           "Please wait before starting another instance of this challenge",
		})
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("failed to check instance cooldown", zap.Error(err), zap.String("challenge_id", challenge.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance eligibility"})
		return
	}

	// teams mode: scope reuse, the concurrency limit, and ownership on the team;
	// keep user_id for attribution.
	ownerCol, ownerArg := "user_id", interface{}(uid)
	if teamID != nil {
		ownerCol, ownerArg = "team_id", interface{}(*teamID)
		// teammates share one admission lock, or two members launching at once
		// both pass the cap and duplicate checks below.
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"anvil-instance-create:team:"+teamID.String()); err != nil {
			h.logger.Error("failed to acquire team admission lock", zap.Error(err), zap.String("team_id", teamID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance eligibility"})
			return
		}
		if economyOn && !instStaff {
			var st economyState
			_ = tx.QueryRow(ctx,
				`SELECT status, expires_at FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
				*teamID, challenge.ID).Scan(&st.status, &st.expiresAt)
			if !economyCanAct(st, time.Now()) {
				c.JSON(http.StatusForbidden, gin.H{"error": economyDenied(st)})
				return
			}
		}
	}

	var existingID uuid.UUID
	err = tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT id FROM instances
		 WHERE %s = $1 AND challenge_id = $2
		   AND status IN ('running', 'creating', 'pending', 'stopping')
		   AND (status = 'stopping' OR expires_at IS NULL OR expires_at > NOW())
		 ORDER BY created_at DESC LIMIT 1`, ownerCol),
		ownerArg, challenge.ID).Scan(&existingID)
	if err == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":       "instance already exists for this challenge",
			"instance_id": existingID.String(),
		})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("failed to check existing challenge instance", zap.Error(err), zap.String("challenge_id", challenge.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance eligibility"})
		return
	}

	if !isAdmin {
		configuredMax := 2
		if h.config != nil && h.config.Container.MaxPerUser > 0 && h.config.Container.MaxPerUser <= 100 {
			configuredMax = h.config.Container.MaxPerUser
		}
		maxInstances, err := h.boundedIntSetting(ctx, tx, "instance.max_per_user", configuredMax, 1, 100)
		if err != nil {
			h.logger.Error("failed to load instance limit setting", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance limit"})
			return
		}

		var activeCount int
		err = tx.QueryRow(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM instances
			 WHERE %s = $1
			   AND status IN ('running', 'creating', 'pending', 'stopping')
			   AND (status = 'stopping' OR expires_at IS NULL OR expires_at > NOW())`, ownerCol), ownerArg).Scan(&activeCount)
		if err != nil {
			h.logger.Error("failed to count active instances", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance limit"})
			return
		}
		if !isStaff(c) && activeCount >= maxInstances {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":       "instance limit reached",
				"max_allowed": maxInstances,
				"active":      activeCount,
			})
			return
		}
	}

	// instance_timeout is stored in minutes
	timeout := h.instanceTimeout(challenge)

	instanceID := uuid.New()
	expiresAt := time.Now().Add(timeout)

	result, err := tx.Exec(ctx,
		`INSERT INTO instances
			(id, user_id, team_id, challenge_id, resource_type, status, created_at, expires_at,
			 reset_count, max_resets)
		 VALUES ($1, $2, $3, $4, $5, 'creating', NOW(), $6, 0, $7)`,
		instanceID, uid, teamID, challenge.ID, challenge.ResourceType, expiresAt, challenge.MaxResets)
	if err != nil {
		h.logger.Error("failed to create instance record", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("instance admission insert affected unexpected row count", zap.Int64("rows", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit instance admission", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}

	instance, opErr := h.provisionInstance(ctx, uid, instanceID, plan, expiresAt, time.Now(), 0)
	if opErr != nil {
		h.writeInstanceOperationError(c, "failed to start instance", opErr)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"instance": instance,
		"message":  "Instance started successfully",
	})
}

// usingK8s reports whether docker challenges route to the GKE instancer.
func (h *InstanceHandler) usingK8s() bool {
	return h.config != nil && h.config.Instancer.Backend == "k8s" && h.instancerSvc != nil
}

// k8sOwnerID is the stable id the per-team instance is keyed on (team in teams
// mode, else the user) — so all members share one instance.
func (h *InstanceHandler) k8sOwnerID(ctx context.Context, uid uuid.UUID) (string, error) {
	_, id, err := h.instanceOwnerScope(ctx, uid)
	if err != nil {
		return "", err
	}
	if u, ok := id.(uuid.UUID); ok {
		return u.String(), nil
	}
	return fmt.Sprintf("%v", id), nil
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func toPortSpecs(pc []instancePortConfig) []instancer.PortSpec {
	out := make([]instancer.PortSpec, 0, len(pc))
	for _, p := range pc {
		out = append(out, instancer.PortSpec{Port: p.Port, Protocol: p.Protocol, Service: p.Service})
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// envPlaceholder matches ${VAR}, ${VAR:-default}, ${VAR:?err} (compose style).
var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[-?][^}]*)?\}`)

// resolveEnvPlaceholders substitutes per-instance values (INSTANCE_ID, FLAG, …)
// into a service's env. Unknown placeholders are left as-is; literals (e.g.
// http://scanner:8081) pass through untouched. Only keys present in subst are
// substituted, so FLAG lands only where the challenge author placed ${FLAG}.
func resolveEnvPlaceholders(env, subst map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = envPlaceholder.ReplaceAllStringFunc(v, func(m string) string {
			name := envPlaceholder.FindStringSubmatch(m)[1]
			if val, ok := subst[name]; ok {
				return val
			}
			return m
		})
	}
	return out
}

// endpointService maps a CR endpoint kind to the ports-map service segment the
// UI splits on ("<port>/<svc>"): tcp-ssl -> tcp (rendered `nc host port`),
// http/https rendered as a URL.
func endpointService(kind string) string {
	switch kind {
	case "http":
		return "http"
	case "https":
		return "https"
	default:
		return "tcp"
	}
}

func (h *InstanceHandler) provisionInstance(
	ctx context.Context,
	uid uuid.UUID,
	instanceID uuid.UUID,
	plan instanceProvisionPlan,
	expiresAt time.Time,
	createdAt time.Time,
	resetCount int,
) (*InstanceResponse, *instanceOperationError) {
	challenge := plan.challenge
	maxExts := 3
	if challenge.MaxExtensions != nil {
		maxExts = *challenge.MaxExtensions
	}
	var instanceIP string
	var resourceID string // container_id, vm_id, or k8s instance id
	var portMappings map[string]int
	var reservedNodeID string
	var reservedVCPU int
	var reservedMemoryMB int
	var k8sEndpoints []instancer.Endpoint

	if challenge.ResourceType == "vm" {
		// no IsAvailable() check: nodes are remote over ssh; availability is
		// verified by the db query below.
		vmTemplate := *plan.vmTemplate

		node, err := h.reserveVMNode(ctx, instanceID, vmTemplate.VCPU, vmTemplate.MemoryMB)
		if errors.Is(err, pgx.ErrNoRows) {
			h.persistCreateFailure(ctx, instanceID, errors.New("no VM node available"))
			return nil, newInstanceOperationError(http.StatusServiceUnavailable, "No VM node available with sufficient capacity", err)
		}
		if err != nil {
			h.logger.Error("no available VM node with capacity", zap.Error(err))
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("reserve VM node: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to reserve VM capacity", err)
		}
		reservedNodeID = node.ID
		reservedVCPU = vmTemplate.VCPU
		reservedMemoryMB = vmTemplate.MemoryMB

		vmInfo, err := h.vmSvc.CreateInstanceOnNode(ctx, challenge.ID, instanceID.String(), &vmTemplate, &node)
		if err != nil {
			h.logger.Error("failed to create VM", zap.Error(err))
			h.cleanupUnpublishedResource(instanceID, challenge.ResourceType, instanceID.String(), node.ID, vmTemplate.VCPU, vmTemplate.MemoryMB)
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("create VM: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to start VM", err)
		}
		if vmInfo == nil || vmInfo.VMID == "" {
			h.logger.Error("VM service returned an empty successful response", zap.String("instance_id", instanceID.String()))
			h.cleanupUnpublishedResource(instanceID, challenge.ResourceType, instanceID.String(), node.ID, vmTemplate.VCPU, vmTemplate.MemoryMB)
			h.persistCreateFailure(ctx, instanceID, errors.New("VM service returned no VM identifier"))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to start VM", errors.New("VM service returned no VM identifier"))
		}

		instanceIP = vmInfo.IPAddress
		resourceID = vmInfo.VMID
	} else if h.usingK8s() {
		ownerID, err := h.k8sOwnerID(ctx, uid)
		if err != nil {
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("resolve instance owner: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to resolve instance owner", err)
		}
		envVars, err := h.generateAndStoreDynamicFlags(ctx, instanceID, uid, challenge.ID)
		if err != nil {
			h.logger.Error("dynamic flag generation failed", zap.Error(err), zap.String("challenge_id", challenge.ID))
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("generate dynamic flags: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to prepare instance flags", err)
		}
		launchSpec := instancer.LaunchSpec{
			TeamID:      ownerID,
			ChallengeID: challenge.ID,
			Slug:        challenge.Slug,
			Image:       challenge.ContainerImage,
			Tag:         challenge.ContainerTag,
			CPULimit:    challenge.CPULimit,
			MemoryLimit: challenge.MemoryLimit,
			Timeout:     h.instanceTimeout(challenge),
			Privesc:     challenge.Privesc,
		}
		if len(plan.services) > 0 {
			// multi-container: resolve per-instance placeholders per role. FLAG is
			// substituted only into the role that declares ${FLAG} in its env, so
			// it never reaches player-facing roles; Flags is left nil so the
			// operator broadcasts nothing.
			subst := map[string]string{
				"INSTANCE_ID":    h.instancerSvc.InstanceID(ownerID, challenge.ID),
				"INTERNAL_TOKEN": randHex(24),
				"FLAG":           envMap(envVars)["FLAG"],
				"PUBLIC_PORT":    "", // resolved post-launch if referenced; unused by current challenges
			}
			for _, svc := range plan.services {
				launchSpec.Containers = append(launchSpec.Containers, instancer.ContainerSpec{
					Name:        svc.Name,
					Image:       svc.Image,
					Tag:         svc.Tag,
					Command:     svc.Command,
					Env:         resolveEnvPlaceholders(svc.Env, subst),
					Ports:       toPortSpecs(svc.Ports),
					Public:      svc.Public,
					Egress:      svc.Egress,
					Privesc:     svc.Privesc,
					CPULimit:    svc.CPULimit,
					MemoryLimit: svc.MemoryLimit,
				})
			}
		} else {
			launchSpec.Ports = toPortSpecs(plan.portConfig)
			launchSpec.Flags = envMap(envVars)
		}
		res, err := h.instancerSvc.Launch(ctx, launchSpec)
		if err != nil {
			h.logger.Error("failed to launch k8s instance", zap.Error(err))
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("launch instance: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to start instance", err)
		}
		resourceID = res.InstanceID
		k8sEndpoints = res.Endpoints
		portMappings = make(map[string]int)
		if len(res.Endpoints) > 0 {
			// ip_address holds the bare host; the ports map is keyed "<port>/<svc>"
			// so the UI reconstructs the exact connect string the operator chose
			// (e.g. `nc web3.h7tex.com 30000`). Source of truth is the CR status.
			instanceIP = res.Endpoints[0].Host
			for _, ep := range res.Endpoints {
				portMappings[fmt.Sprintf("%d/%s", ep.Port, endpointService(ep.Kind))] = ep.Port
			}
		}
	} else {
		containerReq := container.CreateInstanceRequest{
			InstanceID:    instanceID,
			ChallengeSlug: challenge.Slug,
			Image:         challenge.ContainerImage,
			Tag:           challenge.ContainerTag,
			Platform:      challenge.ContainerPlatform,
			CPULimit:      challenge.CPULimit,
			MemoryLimit:   challenge.MemoryLimit,
			Labels: map[string]string{
				"anvil.instance_id":  instanceID.String(),
				"anvil.user_id":      uid.String(),
				"anvil.challenge_id": challenge.ID,
			},
		}

		if len(plan.portConfig) > 0 {
			for _, pc := range plan.portConfig {
				proto := pc.Protocol
				if proto == "" {
					proto = "tcp"
				}
				containerReq.ExposedPorts = append(containerReq.ExposedPorts, container.ExposedPort{
					Port:     pc.Port,
					Protocol: proto,
				})
			}
		}

		envVars, err := h.generateAndStoreDynamicFlags(ctx, instanceID, uid, challenge.ID)
		if err != nil {
			h.logger.Error("dynamic flag generation failed", zap.Error(err), zap.String("challenge_id", challenge.ID))
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("generate dynamic flags: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to prepare instance flags", err)
		}
		containerReq.EnvironmentVars = envVars

		containerInfo, err := h.containerSvc.CreateInstance(ctx, containerReq)
		if err != nil {
			h.logger.Error("failed to create container", zap.Error(err))
			h.persistCreateFailure(ctx, instanceID, fmt.Errorf("create container: %w", err))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to start container", err)
		}
		if containerInfo == nil || containerInfo.ContainerID == "" {
			h.logger.Error("container service returned an empty successful response", zap.String("instance_id", instanceID.String()))
			h.persistCreateFailure(ctx, instanceID, errors.New("container service returned no container identifier"))
			return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to start container", errors.New("container service returned no container identifier"))
		}

		resourceID = containerInfo.ContainerID
		instanceIP = containerInfo.IPAddress

		portMappings = make(map[string]int)
		for i, ep := range containerReq.ExposedPorts {
			svcType := "tcp"
			if i < len(plan.portConfig) && plan.portConfig[i].Service != "" {
				svcType = plan.portConfig[i].Service
			}
			portMappings[fmt.Sprintf("%d/%s", ep.Port, svcType)] = ep.Port
		}
	}

	portsJSON, err := json.Marshal(portMappings)
	if err != nil {
		h.cleanupUnpublishedResource(instanceID, challenge.ResourceType, resourceID, reservedNodeID, reservedVCPU, reservedMemoryMB)
		h.persistCreateFailure(ctx, instanceID, fmt.Errorf("serialize assigned ports: %w", err))
		return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to save instance", err)
	}

	result, err := h.db.Pool.Exec(ctx,
		`UPDATE instances
		 SET container_id = $1, ip_address = $2, assigned_ports = $3,
		     vm_node_id = NULLIF($4, '')::uuid, reserved_vcpu = NULLIF($5, 0),
		     reserved_memory_mb = NULLIF($6, 0), status = 'running',
		     started_at = NOW(), updated_at = NOW(), error_message = NULL
		 WHERE id = $7 AND status = 'creating'`,
		resourceID, instanceIP, portsJSON, reservedNodeID, reservedVCPU, reservedMemoryMB, instanceID)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("final instance update affected %d rows", result.RowsAffected())
		}
		h.logger.Error("failed to publish created instance", zap.Error(err), zap.String("instance_id", instanceID.String()))
		h.cleanupUnpublishedResource(instanceID, challenge.ResourceType, resourceID, reservedNodeID, reservedVCPU, reservedMemoryMB)
		h.persistCreateFailure(ctx, instanceID, fmt.Errorf("publish instance: %w", err))
		return nil, newInstanceOperationError(http.StatusInternalServerError, "failed to save instance", err)
	}

	return &InstanceResponse{
		ID:            instanceID.String(),
		ChallengeID:   challenge.ID,
		ChallengeName: challenge.Name,
		ChallengeSlug: challenge.Slug,
		ContainerID:   resourceID,
		Status:        "running",
		IPAddress:     instanceIP,
		Ports:         portMappings,
		CreatedAt:     createdAt.Unix(),
		ExpiresAt:     expiresAt.Unix(),
		MaxExtensions: maxExts,
		ResetCount:    resetCount,
		MaxResets:     challenge.MaxResets,
		Endpoints:     k8sEndpoints,
	}, nil
}

type instanceRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadAssignedVMNode(ctx context.Context, querier instanceRowQuerier, nodeID uuid.UUID) (*vm.NodeInfo, error) {
	var node vm.NodeInfo
	err := querier.QueryRow(ctx, `
		SELECT id, name, hostname, ip_address, COALESCE(ssh_port, 22),
		       COALESCE(ssh_user, 'anvil'), COALESCE(ssh_key_path, ''),
		       COALESCE(libvirt_uri, 'qemu:///system'), COALESCE(vm_network_name, 'anvil-lab')
		FROM vm_nodes WHERE id = $1`, nodeID).Scan(
		&node.ID, &node.Name, &node.Hostname, &node.IPAddress, &node.SSHPort,
		&node.SSHUser, &node.SSHKeyPath, &node.LibvirtURI, &node.NetworkName,
	)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (h *InstanceHandler) boundedIntSetting(
	ctx context.Context,
	querier instanceRowQuerier,
	key string,
	fallback int,
	minimum int,
	maximum int,
) (int, error) {
	var raw string
	err := querier.QueryRow(ctx,
		`SELECT value #>> '{}' FROM platform_settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query platform setting %q: %w", key, err)
	}

	value, err := parseBoundedPositiveInt(raw, minimum, maximum)
	if err != nil {
		h.logger.Warn("ignoring invalid integer platform setting",
			zap.String("key", key), zap.String("value", raw), zap.Error(err), zap.Int("fallback", fallback))
		return fallback, nil
	}
	return value, nil
}

func (h *InstanceHandler) boolSetting(
	ctx context.Context,
	querier instanceRowQuerier,
	key string,
	fallback bool,
) (bool, error) {
	var raw string
	err := querier.QueryRow(ctx,
		`SELECT value #>> '{}' FROM platform_settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return false, fmt.Errorf("query platform setting %q: %w", key, err)
	}
	value, err := parseBooleanSetting(raw)
	if err != nil {
		h.logger.Warn("ignoring invalid boolean platform setting",
			zap.String("key", key), zap.String("value", raw), zap.Error(err), zap.Bool("fallback", fallback))
		return fallback, nil
	}
	return value, nil
}

func parseBooleanSetting(raw string) (bool, error) {
	return strconv.ParseBool(strings.TrimSpace(raw))
}

func parseBoundedPositiveInt(raw string, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("must be an integer: %w", err)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
	}
	return value, nil
}

func (h *InstanceHandler) persistCreateFailure(ctx context.Context, instanceID uuid.UUID, creationErr error) {
	if creationErr == nil {
		creationErr = errors.New("unknown instance creation failure")
	}
	persist := func(updateCtx context.Context) error {
		result, err := h.db.Pool.Exec(updateCtx,
			`UPDATE instances
			 SET status = 'failed', error_message = $2, updated_at = NOW()
			 WHERE id = $1 AND status = 'creating'`,
			instanceID, creationErr.Error())
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("failure update affected %d rows", result.RowsAffected())
		}
		return nil
	}

	err := persist(ctx)
	if err != nil && ctx.Err() != nil {
		retryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = persist(retryCtx)
	}
	if err == nil {
		return
	}
	h.logger.Error("failed to persist instance creation failure",
		zap.Error(err), zap.String("instance_id", instanceID.String()), zap.NamedError("creation_error", creationErr))
}

func (h *InstanceHandler) reserveVMNode(ctx context.Context, instanceID uuid.UUID, vcpu, memoryMB int) (vm.NodeInfo, error) {
	var node vm.NodeInfo
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		return node, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM vm_nodes
			WHERE status = 'online'
			  AND total_vcpu - COALESCE(used_vcpu, 0) - COALESCE(reserved_vcpu, 0) >= $1
			  AND total_memory_mb - COALESCE(used_memory_mb, 0) - COALESCE(reserved_memory_mb, 0) >= $2
			  AND COALESCE(active_vms, 0) < COALESCE(max_vms, 10)
			ORDER BY priority DESC, COALESCE(active_vms, 0) ASC, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE vm_nodes AS node
		SET used_vcpu = COALESCE(node.used_vcpu, 0) + $1,
		    used_memory_mb = COALESCE(node.used_memory_mb, 0) + $2,
		    active_vms = COALESCE(node.active_vms, 0) + 1,
		    updated_at = NOW()
		FROM candidate
		WHERE node.id = candidate.id
		RETURNING node.id, node.name, node.hostname, node.ip_address,
		          COALESCE(node.ssh_port, 22), COALESCE(node.ssh_user, 'anvil'),
		          COALESCE(node.ssh_key_path, ''), COALESCE(node.libvirt_uri, 'qemu:///system'),
		          COALESCE(node.vm_network_name, 'anvil-lab')`,
		vcpu, memoryMB).Scan(
		&node.ID, &node.Name, &node.Hostname, &node.IPAddress, &node.SSHPort,
		&node.SSHUser, &node.SSHKeyPath, &node.LibvirtURI, &node.NetworkName,
	)
	if err != nil {
		return node, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE instances
		SET vm_node_id = $2, reserved_vcpu = $3, reserved_memory_mb = $4, updated_at = NOW()
		WHERE id = $1 AND status = 'creating'`, instanceID, node.ID, vcpu, memoryMB)
	if err != nil {
		return node, err
	}
	if result.RowsAffected() != 1 {
		return node, fmt.Errorf("VM reservation record affected %d rows", result.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return node, err
	}
	return node, nil
}

func (h *InstanceHandler) releaseVMNodeReservation(ctx context.Context, nodeID string, vcpu, memoryMB int) error {
	parsedNodeID, err := uuid.Parse(nodeID)
	if err != nil {
		return fmt.Errorf("invalid VM node reservation ID: %w", err)
	}
	return releaseVMNodeCapacity(ctx, h.db.Pool, &parsedNodeID, vcpu, memoryMB)
}

type instanceCapacityExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// older instance rows have no reservation metadata, so they're intentionally
// left alone instead of guessing a node or resource size.
func releaseVMNodeCapacity(
	ctx context.Context,
	execer instanceCapacityExecer,
	nodeID *uuid.UUID,
	vcpu int,
	memoryMB int,
) error {
	if nodeID == nil {
		return nil
	}
	if vcpu < 0 || memoryMB < 0 {
		return errors.New("invalid negative VM reservation")
	}
	result, err := execer.Exec(ctx, `
		UPDATE vm_nodes
		SET used_vcpu = GREATEST(0, COALESCE(used_vcpu, 0) - $2),
		    used_memory_mb = GREATEST(0, COALESCE(used_memory_mb, 0) - $3),
		    active_vms = GREATEST(0, COALESCE(active_vms, 0) - 1),
		    updated_at = NOW()
		WHERE id = $1`, *nodeID, vcpu, memoryMB)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("VM node reservation release affected %d rows", result.RowsAffected())
	}
	return nil
}

func (h *InstanceHandler) cleanupUnpublishedResource(
	instanceID uuid.UUID,
	resourceType string,
	resourceID string,
	nodeID string,
	vcpu int,
	memoryMB int,
) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cleanupErr error
	switch resourceType {
	case "vm":
		if h.vmSvc == nil {
			cleanupErr = errors.New("VM service unavailable during cleanup")
		} else if resourceID != "" {
			parsedNodeID, parseErr := uuid.Parse(nodeID)
			if parseErr != nil {
				cleanupErr = fmt.Errorf("invalid assigned VM node: %w", parseErr)
			} else if node, loadErr := loadAssignedVMNode(cleanupCtx, h.db.Pool, parsedNodeID); loadErr != nil {
				cleanupErr = fmt.Errorf("load assigned VM node: %w", loadErr)
			} else {
				cleanupErr = h.vmSvc.DestroyInstanceByNameOnNode(cleanupCtx, resourceID, node)
			}
		}
	case "docker":
		if h.usingK8s() {
			if resourceID != "" {
				cleanupErr = h.instancerSvc.Destroy(cleanupCtx, resourceID)
			}
		} else if h.containerSvc == nil {
			cleanupErr = errors.New("container service unavailable during cleanup")
		} else if resourceID != "" {
			cleanupErr = h.containerSvc.StopInstance(cleanupCtx, resourceID)
		}
	}
	if cleanupErr != nil {
		h.logger.Error("failed to clean up unpublished runtime",
			zap.Error(cleanupErr), zap.String("instance_id", instanceID.String()), zap.String("resource_id", resourceID))
		// a vm may still be consuming the reservation when cleanup is uncertain.
		return
	}
	if resourceType == "vm" && nodeID != "" {
		parsedNodeID, err := uuid.Parse(nodeID)
		if err != nil {
			h.logger.Error("failed to parse unpublished VM reservation node",
				zap.Error(err), zap.String("instance_id", instanceID.String()), zap.String("node_id", nodeID))
			return
		}
		tx, err := h.db.Pool.Begin(cleanupCtx)
		if err == nil {
			var result pgconn.CommandTag
			result, err = tx.Exec(cleanupCtx, `
				UPDATE instances SET vm_node_id = NULL, reserved_vcpu = NULL,
				       reserved_memory_mb = NULL, updated_at = NOW()
				WHERE id = $1 AND vm_node_id = $2`, instanceID, parsedNodeID)
			if err == nil && result.RowsAffected() != 1 {
				err = fmt.Errorf("reservation ownership update affected %d rows", result.RowsAffected())
			}
			if err == nil {
				err = releaseVMNodeCapacity(cleanupCtx, tx, &parsedNodeID, vcpu, memoryMB)
			}
			if err == nil {
				err = tx.Commit(cleanupCtx)
			} else {
				_ = tx.Rollback(cleanupCtx)
			}
		}
		if err != nil {
			h.logger.Error("failed to release unpublished VM reservation",
				zap.Error(err), zap.String("instance_id", instanceID.String()), zap.String("node_id", nodeID))
		}
	}
}

func (h *InstanceHandler) Get(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	instanceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instance ID"})
		return
	}

	// teams mode: any member may view the team's instance.
	ownerCol, ownerArg, err := h.instanceOwnerScope(c.Request.Context(), uid)
	if err != nil {
		h.logger.Error("failed to resolve instance owner scope", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instance"})
		return
	}

	query := fmt.Sprintf(`
		SELECT
			i.id, i.challenge_id, i.container_id, i.status,
			i.ip_address, i.assigned_ports, i.created_at, i.expires_at,
			i.extensions_used, COALESCE(c.max_extensions, 3) as max_extensions,
			COALESCE(i.reset_count, 0), COALESCE(i.max_resets, c.max_resets, 3),
			c.name as challenge_name, c.slug as challenge_slug
		FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.id = $1 AND i.%s = $2
	`, ownerCol)

	var inst InstanceResponse
	var portsJSON []byte
	var createdAt, expiresAt time.Time
	var ipAddress, containerID *string

	err = h.db.Pool.QueryRow(c.Request.Context(), query, instanceID, ownerArg).Scan(
		&inst.ID, &inst.ChallengeID, &containerID, &inst.Status,
		&ipAddress, &portsJSON, &createdAt, &expiresAt,
		&inst.ExtensionsUsed, &inst.MaxExtensions,
		&inst.ResetCount, &inst.MaxResets,
		&inst.ChallengeName, &inst.ChallengeSlug,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to get instance", zap.String("instance_id", instanceID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instance"})
		return
	}

	if containerID != nil {
		inst.ContainerID = *containerID
	}
	if ipAddress != nil {
		inst.IPAddress = *ipAddress
	}
	inst.CreatedAt = createdAt.Unix()
	inst.ExpiresAt = expiresAt.Unix()
	if len(portsJSON) > 0 {
		if err := json.Unmarshal(portsJSON, &inst.Ports); err != nil {
			h.logger.Error("failed to parse assigned_ports", zap.String("instance_id", inst.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instance"})
			return
		}
	}
	if inst.Ports == nil {
		inst.Ports = make(map[string]int)
	}

	c.JSON(http.StatusOK, inst)
}

func (h *InstanceHandler) Extend(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	instanceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instance ID"})
		return
	}
	ctx := c.Request.Context()

	// teams mode: any member may extend the team's instance.
	ownerCol, ownerArg, err := h.instanceOwnerScope(ctx, uid)
	if err != nil {
		h.logger.Error("failed to resolve instance owner scope", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}

	extensionMinutes, err := h.boundedIntSetting(ctx, h.db.Pool, "instance.extension_minutes", 30, 1, 24*60)
	if err != nil {
		h.logger.Error("failed to load instance extension setting", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin instance extend transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var newExpiry time.Time
	var extensionsUsed, maxExtensions int
	var runtimeID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH extended AS (
			UPDATE instances i
			SET expires_at = COALESCE(i.expires_at, NOW()) + ($3 * INTERVAL '1 minute'),
			    extensions_used = COALESCE(i.extensions_used, 0) + 1,
			    updated_at = NOW()
			FROM challenges ch
			WHERE i.challenge_id = ch.id
			  AND i.id = $1 AND i.%s = $2 AND i.status = 'running'
			  AND COALESCE(i.extensions_used, 0) < COALESCE(ch.max_extensions, 3)
			RETURNING i.expires_at, i.extensions_used, COALESCE(ch.max_extensions, 3) AS max_extensions,
			          COALESCE(i.container_id, '') AS container_id, ch.resource_type::text AS resource_type
		)
		SELECT expires_at, extensions_used, max_extensions,
		       CASE WHEN resource_type = 'docker' THEN container_id ELSE '' END FROM extended
	`, ownerCol), instanceID, ownerArg, extensionMinutes).Scan(&newExpiry, &extensionsUsed, &maxExtensions, &runtimeID)
	if err == nil {
		// on k8s the operator reaps by the CR's own expiry, so move it too or
		// the pod dies at the old deadline while the UI shows the new one.
		if runtimeID != "" && h.instancerSvc != nil && h.instancerSvc.Enabled() {
			if perr := h.instancerSvc.Extend(ctx, runtimeID, newExpiry); perr != nil {
				h.logger.Error("failed to extend instance runtime", zap.Error(perr), zap.String("runtime_id", runtimeID))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
				return
			}
		}
		if cerr := tx.Commit(ctx); cerr != nil {
			h.logger.Error("failed to commit instance extension", zap.Error(cerr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"message":              "Instance extended successfully",
			"new_expires_at":       newExpiry.Unix(),
			"extension_minutes":    extensionMinutes,
			"extensions_used":      extensionsUsed,
			"extensions_remaining": maxExtensions - extensionsUsed,
		})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("failed to extend instance", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}

	var status string
	err = h.db.Pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT i.status, COALESCE(i.extensions_used, 0), COALESCE(ch.max_extensions, 3)
		FROM instances i JOIN challenges ch ON ch.id = i.challenge_id
		WHERE i.id = $1 AND i.%s = $2
	`, ownerCol), instanceID, ownerArg).Scan(&status, &extensionsUsed, &maxExtensions)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to inspect unextended instance", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}
	if status != "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "instance is not running"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "max extensions reached", "used": extensionsUsed, "max": maxExtensions})
}

func validateRevertState(status string, resetCount, maxResets int, runtimeID *string) *instanceOperationError {
	if status != "running" {
		return newInstanceOperationError(http.StatusConflict, "instance is not running", nil)
	}
	if resetCount < 0 || maxResets < 0 {
		return newInstanceOperationError(http.StatusInternalServerError, "instance has an invalid reset limit", nil)
	}
	if resetCount >= maxResets {
		err := newInstanceOperationError(http.StatusConflict, "reset limit reached", nil)
		err.body["reset_count"] = resetCount
		err.body["max_resets"] = maxResets
		return err
	}
	if runtimeID == nil || strings.TrimSpace(*runtimeID) == "" {
		return newInstanceOperationError(http.StatusConflict, "instance runtime is unavailable", nil)
	}
	return nil
}

func (h *InstanceHandler) destroyInstanceRuntime(ctx context.Context, inst instanceRuntime) error {
	if inst.RuntimeID == nil || strings.TrimSpace(*inst.RuntimeID) == "" {
		return nil
	}
	runtimeID := *inst.RuntimeID
	switch inst.ResourceType {
	case "vm":
		if h.vmSvc == nil {
			return errors.New("VM service unavailable")
		}
		if inst.VMNodeID != nil {
			node, err := loadAssignedVMNode(ctx, h.db.Pool, *inst.VMNodeID)
			if err != nil {
				return fmt.Errorf("load assigned VM node: %w", err)
			}
			return h.vmSvc.DestroyInstanceByNameOnNode(ctx, runtimeID, node)
		}
		return h.vmSvc.DestroyInstanceByName(ctx, runtimeID)
	case "docker":
		if h.usingK8s() {
			return h.instancerSvc.Destroy(ctx, runtimeID)
		}
		if h.containerSvc == nil {
			return errors.New("container service unavailable")
		}
		return h.containerSvc.StopInstance(ctx, runtimeID)
	default:
		return fmt.Errorf("unsupported resource type %q", inst.ResourceType)
	}
}

func (h *InstanceHandler) restoreClaimedInstance(ctx context.Context, uid, instanceID uuid.UUID, status string) error {
	result, err := h.db.Pool.Exec(ctx,
		`UPDATE instances SET status = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'stopping'`,
		instanceID, uid, status)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("restore affected %d rows", result.RowsAffected())
	}
	return nil
}

// destroys and recreates an owned running instance with no user-stop cooldown.
// every replacement param is server-derived from the claimed instance, so
// there's no client-controlled cooldown bypass.
func (h *InstanceHandler) Revert(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	instanceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instance ID"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin instance revert", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert instance"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"anvil-instance-create:"+uid.String()); err != nil {
		h.logger.Error("failed to lock instance revert", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert instance"})
		return
	}

	var inst instanceRuntime
	var challengeSlug string
	var createdAt time.Time
	var resetCount, maxResets int
	err = tx.QueryRow(ctx, `
		SELECT i.id, i.container_id, i.status, i.challenge_id, i.resource_type,
		       i.vm_node_id, COALESCE(i.reserved_vcpu, 0),
		       COALESCE(i.reserved_memory_mb, 0), i.created_at,
		       COALESCE(i.reset_count, 0), COALESCE(i.max_resets, ch.max_resets, 3),
		       ch.slug
		FROM instances i
		JOIN challenges ch ON ch.id = i.challenge_id
		WHERE i.id = $1 AND i.user_id = $2
		FOR UPDATE OF i`, instanceID, uid).Scan(
		&inst.ID, &inst.RuntimeID, &inst.Status, &inst.ChallengeID, &inst.ResourceType,
		&inst.VMNodeID, &inst.ReservedVCPU, &inst.ReservedMemoryMB, &createdAt,
		&resetCount, &maxResets, &challengeSlug,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load instance for revert", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance"})
		return
	}
	if opErr := validateRevertState(inst.Status, resetCount, maxResets, inst.RuntimeID); opErr != nil {
		c.JSON(opErr.status, opErr.body)
		return
	}

	challenge, err := h.loadPublishedChallenge(ctx, tx, challengeSlug, isStaff(c))
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusConflict, gin.H{"error": "challenge is no longer available"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load challenge for revert", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load challenge"})
		return
	}
	if challenge.ID != inst.ChallengeID || challenge.ResourceType != inst.ResourceType {
		h.logger.Error("instance challenge metadata is inconsistent", zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "instance configuration is invalid"})
		return
	}
	challenge.MaxResets = maxResets

	if opErr := h.checkVPNEligibility(ctx, tx, uid); opErr != nil {
		h.writeInstanceOperationError(c, "failed to check revert eligibility", opErr)
		return
	}
	plan, opErr := h.prepareProvisionPlan(ctx, tx, challenge)
	if opErr != nil {
		h.writeInstanceOperationError(c, "failed to prepare instance revert", opErr)
		return
	}

	result, err := tx.Exec(ctx,
		`UPDATE instances SET status = 'stopping', updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'running'
		   AND expires_at > NOW()
		   AND COALESCE(reset_count, 0) = $3
		   AND COALESCE(reset_count, 0) < $4`, instanceID, uid, resetCount, maxResets)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("revert claim affected %d rows", result.RowsAffected())
		}
		h.logger.Error("failed to claim instance for revert", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusConflict, gin.H{"error": "instance state changed; try again"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit instance revert claim", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert instance"})
		return
	}

	// once the claim is committed, finish the lifecycle even if the client
	// disconnects, or a cancelled request strands a stopping row and a
	// half-destroyed runtime.
	operationCtx, operationCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer operationCancel()

	if err := h.destroyInstanceRuntime(operationCtx, inst); err != nil {
		h.logger.Error("failed to destroy instance runtime for revert", zap.Error(err), zap.String("instance_id", instanceID.String()))
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if restoreErr := h.restoreClaimedInstance(restoreCtx, uid, instanceID, "running"); restoreErr != nil {
			h.logger.Error("failed to restore instance after revert failure", zap.Error(restoreErr), zap.String("instance_id", instanceID.String()))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert instance"})
		return
	}

	expiresAt := time.Now().Add(h.instanceTimeout(challenge))
	previousResetCount := resetCount
	prepTx, err := h.db.Pool.Begin(operationCtx)
	if err != nil {
		h.logger.Error("failed to prepare destroyed instance for replacement", zap.Error(err), zap.String("instance_id", instanceID.String()))
		h.persistRevertFailure(instanceID, "failed to prepare replacement")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "original instance stopped, but replacement preparation failed"})
		return
	}
	defer func() { _ = prepTx.Rollback(operationCtx) }()

	err = prepTx.QueryRow(operationCtx, `
		UPDATE instances
		SET container_id = NULL, container_name = NULL, vm_id = NULL,
		    ip_address = NULL, assigned_ports = '{}'::jsonb,
		    vm_node_id = NULL, reserved_vcpu = NULL, reserved_memory_mb = NULL,
		    status = 'creating', started_at = NULL, stopped_at = NULL,
		    expires_at = $3, extensions_used = 0,
		    reset_count = COALESCE(reset_count, 0) + 1,
		    max_resets = COALESCE(max_resets, $5),
		    updated_at = NOW(), error_message = NULL
		WHERE id = $1 AND user_id = $2 AND status = 'stopping'
		  AND COALESCE(reset_count, 0) = $4
		  AND COALESCE(max_resets, $5) = $5
		  AND COALESCE(reset_count, 0) < $5
		RETURNING reset_count, COALESCE(max_resets, 3)`,
		instanceID, uid, expiresAt, previousResetCount, maxResets).Scan(&resetCount, &maxResets)
	if err == nil && inst.ResourceType == "vm" {
		err = releaseVMNodeCapacity(operationCtx, prepTx, inst.VMNodeID, inst.ReservedVCPU, inst.ReservedMemoryMB)
	}
	if err == nil {
		err = prepTx.Commit(operationCtx)
	}
	if err != nil {
		h.logger.Error("failed to prepare instance replacement", zap.Error(err), zap.String("instance_id", instanceID.String()))
		h.persistRevertFailure(instanceID, "failed to prepare replacement")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "original instance stopped, but replacement preparation failed"})
		return
	}

	instance, opErr := h.provisionInstance(operationCtx, uid, instanceID, plan, expiresAt, createdAt, resetCount)
	if opErr != nil {
		if message, ok := opErr.body["error"].(string); ok {
			opErr.body["error"] = "original instance stopped, but replacement failed: " + message
		}
		opErr.body["original_stopped"] = true
		h.writeInstanceOperationError(c, "failed to provision reverted instance", opErr)
		return
	}
	h.logAction(c, uid.String(), "instance_reverted", map[string]interface{}{
		"instance_id":  instanceID.String(),
		"challenge_id": inst.ChallengeID,
		"reset_count":  resetCount,
	})
	c.JSON(http.StatusOK, gin.H{
		"message":          "Instance reverted successfully",
		"instance":         instance,
		"reset_count":      resetCount,
		"max_resets":       maxResets,
		"resets_remaining": max(0, maxResets-resetCount),
	})
}

func (h *InstanceHandler) persistRevertFailure(instanceID uuid.UUID, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := h.db.Pool.Exec(ctx,
		`UPDATE instances
		 SET status = 'failed', error_message = $2, updated_at = NOW()
		 WHERE id = $1 AND status IN ('stopping', 'creating')`, instanceID, message)
	if err != nil || result.RowsAffected() != 1 {
		h.logger.Error("failed to persist instance revert failure", zap.Error(err),
			zap.Int64("rows_affected", result.RowsAffected()), zap.String("instance_id", instanceID.String()))
	}
}

func (h *InstanceHandler) Stop(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	instanceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instance ID"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	h.logger.Info("Stop handler called", zap.String("user_id", uid.String()), zap.String("instance_id", instanceID.String()))

	// lock and claim the row before touching its runtime; Revert uses the same
	// state transition, so concurrent Stop/Revert can't both destroy it.
	claimTx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin instance stop claim", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	defer func() { _ = claimTx.Rollback(ctx) }()

	var inst instanceRuntime
	err = claimTx.QueryRow(ctx,
		`SELECT i.container_id, i.status, i.challenge_id, i.resource_type,
		        i.vm_node_id, COALESCE(i.reserved_vcpu, 0), COALESCE(i.reserved_memory_mb, 0)
		 FROM instances i
		 WHERE i.id = $1 AND i.user_id = $2
		 FOR UPDATE`,
		instanceID, uid).Scan(
		&inst.RuntimeID, &inst.Status, &inst.ChallengeID, &inst.ResourceType,
		&inst.VMNodeID, &inst.ReservedVCPU, &inst.ReservedMemoryMB,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// idempotent: the instance is already gone (reaped/cleaned). don't error -
		// report success so the player can relaunch instead of getting stuck.
		h.logger.Warn("stop on missing instance (idempotent success)", zap.String("instance_id", instanceID.String()), zap.String("user_id", uid.String()))
		c.JSON(http.StatusOK, gin.H{"status": "stopped", "already_gone": true, "message": "instance already stopped"})
		return
	}
	if err != nil {
		h.logger.Error("instance query failed in Stop handler", zap.Error(err), zap.String("instance_id", instanceID.String()), zap.String("user_id", uid.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance"})
		return
	}

	if inst.Status == "stopped" || inst.Status == "stopping" || inst.Status == "expired" {
		// idempotent: already stopping/stopped/expired -> success, so relaunch is unblocked.
		c.JSON(http.StatusOK, gin.H{"status": "stopped", "already_stopped": true, "message": "instance already stopped"})
		return
	}
	inst.ID = instanceID
	result, err := claimTx.Exec(ctx,
		`UPDATE instances SET status = 'stopping', updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = $3`, instanceID, uid, inst.Status)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("stop claim affected %d rows", result.RowsAffected())
		}
		h.logger.Error("failed to claim instance for stop", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusConflict, gin.H{"error": "instance state changed; try again"})
		return
	}
	if err := claimTx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit instance stop claim", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}

	h.logger.Info("stopping instance",
		zap.String("instance_id", instanceID.String()),
		zap.String("status", inst.Status),
		zap.String("resource_type", inst.ResourceType),
		zap.Bool("has_container_id", inst.RuntimeID != nil && *inst.RuntimeID != ""))

	if err := h.destroyInstanceRuntime(ctx, inst); err != nil {
		h.logger.Error("failed to destroy instance runtime", zap.Error(err), zap.String("instance_id", instanceID.String()))
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer restoreCancel()
		if restoreErr := h.restoreClaimedInstance(restoreCtx, uid, instanceID, inst.Status); restoreErr != nil {
			h.logger.Error("failed to restore instance after stop failure", zap.Error(restoreErr), zap.String("instance_id", instanceID.String()))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}

	// commit the record removal, exact capacity release, and cooldown together.
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin instance stop transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// keep the row: its dynamic flags cascade with it, and a player who stops
	// an instance to free a slot must still be able to submit what they found.
	result, err = tx.Exec(ctx,
		`UPDATE instances SET status = 'stopped', updated_at = NOW() WHERE id = $1 AND user_id = $2 AND status = 'stopping'`, instanceID, uid)
	if err != nil {
		h.logger.Error("failed to delete instance", zap.Error(err), zap.String("instance_id", instanceID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete instance"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("instance deletion affected an unexpected number of rows", zap.String("instance_id", instanceID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete instance"})
		return
	}
	h.logger.Info("instance stopped", zap.String("instance_id", instanceID.String()))

	if inst.ResourceType == "vm" {
		if updateErr := releaseVMNodeCapacity(ctx, tx, inst.VMNodeID, inst.ReservedVCPU, inst.ReservedMemoryMB); updateErr != nil {
			h.logger.Error("failed to release VM node capacity", zap.Error(updateErr), zap.String("instance_id", instanceID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to release instance capacity"})
			return
		}
	}

	var cooldownMinutes int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(cooldown_minutes, 15) FROM challenges WHERE id = $1`,
		inst.ChallengeID).Scan(&cooldownMinutes)
	if err != nil {
		h.logger.Error("failed to load instance cooldown", zap.String("challenge_id", inst.ChallengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}

	cooldownUntil := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
	result, err = tx.Exec(ctx,
		`INSERT INTO user_cooldowns (id, user_id, challenge_id, cooldown_until, created_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, challenge_id) DO UPDATE SET cooldown_until = $4`,
		uuid.New(), uid, inst.ChallengeID, cooldownUntil)
	if err != nil {
		h.logger.Error("failed to set cooldown", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("cooldown update affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit instance stop", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}

	h.logAction(c, uid.String(), "instance_stopped", map[string]interface{}{
		"instance_id":    instanceID,
		"challenge_id":   inst.ChallengeID,
		"cooldown_until": cooldownUntil.Unix(),
	})

	c.JSON(http.StatusOK, gin.H{
		"message":          "Instance stopped successfully",
		"cooldown_until":   cooldownUntil.Unix(),
		"cooldown_minutes": cooldownMinutes,
	})
}

func (h *InstanceHandler) logAction(c *gin.Context, userID, action string, details map[string]interface{}) {
	if err := logAdminAction(h.db, c, userID, action, "instance", "", details); err != nil {
		h.logger.Warn("failed to log instance action", zap.Error(err))
	}
}

func (h *InstanceHandler) Delete(c *gin.Context) {
	h.Stop(c)
}

// generates a unique PREFIX{uuid} value per dynamic flag, persists them in
// instance_flags, and returns the docker env vars to inject: FLAG=<value>
// (first/only flag) and FLAG_<UPPER_SLUG>=<value> per flag by sanitised name.
// static flags are ignored — they live in flag_hash and need no injection.
func (h *InstanceHandler) generateAndStoreDynamicFlags(
	ctx context.Context,
	instanceID uuid.UUID,
	userID uuid.UUID,
	challengeID string,
) ([]string, error) {
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning dynamic flag transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, name, dynamic_flag_prefix
		   FROM flags
		  WHERE challenge_id = $1 AND flag_type = 'dynamic'
		  ORDER BY sort_order, id`,
		challengeID)
	if err != nil {
		return nil, fmt.Errorf("querying dynamic flags: %w", err)
	}
	defer rows.Close()

	type dynFlag struct {
		ID     string
		Name   string
		Prefix string // resolved prefix, e.g. "H7CTF"
	}
	var dynamicFlags []dynFlag
	for rows.Next() {
		var id, name string
		var prefix *string
		if err := rows.Scan(&id, &name, &prefix); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning dynamic flag: %w", err)
		}
		p := "FLAG"
		if prefix != nil && *prefix != "" {
			p = *prefix
		}
		dynamicFlags = append(dynamicFlags, dynFlag{ID: id, Name: name, Prefix: p})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating dynamic flags: %w", err)
	}
	rows.Close()

	if len(dynamicFlags) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("committing empty dynamic flag transaction: %w", err)
		}
		return nil, nil
	}

	var envVars []string
	for i, df := range dynamicFlags {
		flagValue := fmt.Sprintf("%s{%s}", df.Prefix, uuid.New().String())

		// on retry, preserve and return the already-injected value instead of
		// one that disagrees with the db.
		var storedValue string
		err := tx.QueryRow(ctx,
			`INSERT INTO instance_flags
				(id, instance_id, user_id, challenge_id, flag_id, flag_value, created_at)
			VALUES
				($6, $1, $2, $3, $4, $5, NOW())
			ON CONFLICT (instance_id, flag_id) DO UPDATE
			SET flag_value = instance_flags.flag_value
			RETURNING flag_value`,
			instanceID, userID, challengeID, df.ID, flagValue, uuid.New()).Scan(&storedValue)
		if err != nil {
			return nil, fmt.Errorf("storing dynamic flag %s: %w", df.ID, err)
		}

		// first dynamic flag → canonical FLAG env var
		if i == 0 {
			envVars = append(envVars, "FLAG="+storedValue)
		}
		// name-scoped var for multi-flag challenges: FLAG_TOKEN_OVERFLOW=...
		envVars = append(envVars, fmt.Sprintf("%s=%s", dynamicFlagEnvName(df.Name), storedValue))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing dynamic flags: %w", err)
	}
	return envVars, nil
}

func dynamicFlagEnvName(name string) string {
	slug := strings.ToUpper(strings.ReplaceAll(name, " ", "_"))
	slug = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, slug)
	return "FLAG_" + slug
}
