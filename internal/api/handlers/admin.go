package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"go.uber.org/zap"
)

// AdminService handles admin operations
type AdminService struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	logger       *zap.Logger
}

// NewAdminService creates a new admin service
func NewAdminService(cfg *config.Config, db *database.DB, containerSvc *container.Service, logger *zap.Logger) *AdminService {
	return &AdminService{config: cfg, db: db, containerSvc: containerSvc, logger: logger}
}

// ListUsers returns all users (admin)
func (h *AdminUserHandler) List(c *gin.Context) {
	query := `
		SELECT id, username, email, role, total_score, created_at
		FROM users
		ORDER BY created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list users", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch users"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id, username, email, role string
		var totalScore int
		var createdAt time.Time

		if err := rows.Scan(&id, &username, &email, &role, &totalScore, &createdAt); err != nil {
			h.logger.Error("failed to scan user", zap.Error(err))
			continue
		}

		users = append(users, gin.H{
			"id":          id,
			"username":    username,
			"email":       email,
			"role":        role,
			"total_score": totalScore,
			"created_at":  createdAt.Unix(),
		})
	}

	if users == nil {
		users = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"users": users, "total": len(users)})
}

// GetUser returns a specific user
func (h *AdminUserHandler) Get(c *gin.Context) {
	userID := c.Param("id")

	var user struct {
		ID         string
		Username   string
		Email      string
		Role       string
		TotalScore int
		IsBanned   bool
		CreatedAt  time.Time
	}

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, username, email, role, total_score, is_banned, created_at
		 FROM users WHERE id = $1`, userID).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role,
		&user.TotalScore, &user.IsBanned, &user.CreatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          user.ID,
		"username":    user.Username,
		"email":       user.Email,
		"role":        user.Role,
		"total_score": user.TotalScore,
		"is_banned":   user.IsBanned,
		"created_at":  user.CreatedAt.Unix(),
	})
}

// UpdateUser updates a user
func (h *AdminUserHandler) Update(c *gin.Context) {
	userID := c.Param("id")

	var req struct {
		Role       *string `json:"role"`
		TotalScore *int    `json:"total_score"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Role != nil {
		newRole := strings.ToLower(strings.TrimSpace(*req.Role))
		if newRole != "admin" && newRole != "user" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role: must be 'admin' or 'user'"})
			return
		}

		var currentRole string
		err := h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT role FROM users WHERE id = $1`,
			userID,
		).Scan(&currentRole)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}

		if currentRole == "admin" && newRole != "admin" {
			adminCount, err := h.countAdminUsers(c.Request.Context())
			if err != nil {
				h.logger.Error("failed to count admins", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			if adminCount <= 1 {
				c.JSON(http.StatusConflict, gin.H{"error": "cannot demote the last admin"})
				return
			}
		}

		_, err = h.db.Pool.Exec(c.Request.Context(),
			`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`,
			newRole, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if req.TotalScore != nil {
		_, err := h.db.Pool.Exec(c.Request.Context(),
			`UPDATE users SET total_score = $1, updated_at = NOW() WHERE id = $2`,
			*req.TotalScore, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "user updated"})
}

// BanUser bans a user
func (h *AdminUserHandler) Ban(c *gin.Context) {
	userID := c.Param("id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET is_banned = true, updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user banned"})
}

// UnbanUser unbans a user
func (h *AdminUserHandler) Unban(c *gin.Context) {
	userID := c.Param("id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET is_banned = false, updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unban user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user unbanned"})
}

// DeleteUser deletes a user
func (h *AdminUserHandler) Delete(c *gin.Context) {
	userID := c.Param("id")

	var currentRole string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT role FROM users WHERE id = $1`,
		userID,
	).Scan(&currentRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if currentRole == "admin" {
		adminCount, err := h.countAdminUsers(c.Request.Context())
		if err != nil {
			h.logger.Error("failed to count admins", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
			return
		}
		if adminCount <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot delete the last admin"})
			return
		}
	}

	_, err = h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

func (h *AdminUserHandler) countAdminUsers(ctx context.Context) (int, error) {
	var count int
	err := h.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// FlagInput represents a flag in the create challenge request
type FlagInput struct {
	Name              string `json:"name" binding:"required"`
	Description       string `json:"description"`
	Flag              string `json:"flag"` // required for static; leave empty for dynamic
	Points            int    `json:"points" binding:"required"`
	SortOrder         int    `json:"sort_order"`
	FlagType          string `json:"flag_type"`           // "static" (default) | "dynamic"
	DynamicFlagPrefix string `json:"dynamic_flag_prefix"` // e.g. "H7CTF" → "H7CTF{uuid}"
}

// CreateChallengeRequest represents the request to create a challenge
type CreateChallengeRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Difficulty  string  `json:"difficulty" binding:"required"`
	CategoryID  *string `json:"category_id"`
	Category    string  `json:"category"` // human-readable name; auto-resolved to category_id

	// Challenge type: "docker" or "vm"
	ChallengeType string `json:"challenge_type"`

	// Docker-specific fields
	ContainerImage    string `json:"container_image"`
	ContainerTag      string `json:"container_tag"`
	ContainerPlatform string `json:"container_platform"` // e.g. "linux/amd64" for cross-arch
	CPULimit          string `json:"cpu_limit"`
	MemoryLimit       string `json:"memory_limit"`
	ExposedPorts      []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Service  string `json:"service"`
	} `json:"exposed_ports"`

	// VM-specific fields
	VMTemplateID *string `json:"vm_template_id"`
	VCPU         int     `json:"vcpu"`
	MemoryMB     int     `json:"memory_mb"`

	// Timer and cooldown settings (author-defined)
	VMTimeoutMinutes   *int `json:"vm_timeout_minutes"`   // nil = use difficulty default
	VMMaxExtensions    *int `json:"vm_max_extensions"`    // default 2
	VMExtensionMinutes *int `json:"vm_extension_minutes"` // default 30
	CooldownMinutes    *int `json:"cooldown_minutes"`     // default 10

	// Common fields
	BasePoints      int     `json:"base_points"`
	InstanceTimeout *int    `json:"instance_timeout"`
	MaxExtensions   *int    `json:"max_extensions"`
	AuthorName      string  `json:"author_name"`
	ResourceType    *string `json:"resource_type"` // "docker" or "vm"

	// Multiple flags support
	Flags []FlagInput `json:"flags"`

	// Legacy single flag support (for backward compatibility)
	Flag string `json:"flag"`
}

// ListChallenges returns all challenges (admin)
func (h *AdminChallengeHandler) List(c *gin.Context) {
	query := `
		SELECT c.id, c.name, c.slug, c.description, c.difficulty, c.status, c.base_points,
		       c.total_solves, c.total_flags, c.resource_type, c.created_at,
		       c.category_id, cat.name AS category_name,
		       c.container_image, c.container_tag, c.container_platform,
		       c.cpu_limit, c.memory_limit, c.exposed_ports,
		       c.instance_timeout, c.max_extensions, c.cooldown_minutes,
		       c.author_name
		FROM challenges c
		LEFT JOIN categories cat ON cat.id = c.category_id
		ORDER BY c.created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenges"})
		return
	}
	defer rows.Close()

	var challenges []gin.H
	for rows.Next() {
		var ch struct {
			ID                string
			Name              string
			Slug              string
			Description       *string
			Difficulty        string
			Status            string
			BasePoints        int
			TotalSolves       int
			TotalFlags        int
			ResourceType      string
			CreatedAt         time.Time
			CategoryID        *string
			CategoryName      *string
			ContainerImage    *string
			ContainerTag      *string
			ContainerPlatform *string
			CPULimit          *string
			MemoryLimit       *string
			ExposedPorts      []byte
			InstanceTimeout   *int
			MaxExtensions     *int
			CooldownMinutes   *int
			AuthorName        *string
		}

		if err := rows.Scan(
			&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty,
			&ch.Status, &ch.BasePoints, &ch.TotalSolves, &ch.TotalFlags, &ch.ResourceType, &ch.CreatedAt,
			&ch.CategoryID, &ch.CategoryName,
			&ch.ContainerImage, &ch.ContainerTag, &ch.ContainerPlatform,
			&ch.CPULimit, &ch.MemoryLimit, &ch.ExposedPorts,
			&ch.InstanceTimeout, &ch.MaxExtensions, &ch.CooldownMinutes,
			&ch.AuthorName,
		); err != nil {
			h.logger.Warn("failed to scan challenge row", zap.Error(err))
			continue
		}

		// Parse exposed ports JSON
		var exposedPorts interface{}
		if len(ch.ExposedPorts) > 0 {
			_ = json.Unmarshal(ch.ExposedPorts, &exposedPorts)
		}

		challenges = append(challenges, gin.H{
			"id":                 ch.ID,
			"name":               ch.Name,
			"slug":               ch.Slug,
			"description":        ch.Description,
			"difficulty":         ch.Difficulty,
			"status":             ch.Status,
			"base_points":        ch.BasePoints,
			"total_solves":       ch.TotalSolves,
			"total_flags":        ch.TotalFlags,
			"resource_type":      ch.ResourceType,
			"created_at":         ch.CreatedAt.Unix(),
			"category_id":        ch.CategoryID,
			"category_name":      ch.CategoryName,
			"container_image":    ch.ContainerImage,
			"container_tag":      ch.ContainerTag,
			"container_platform": ch.ContainerPlatform,
			"cpu_limit":          ch.CPULimit,
			"memory_limit":       ch.MemoryLimit,
			"exposed_ports":      exposedPorts,
			"instance_timeout":   ch.InstanceTimeout,
			"max_extensions":     ch.MaxExtensions,
			"cooldown_minutes":   ch.CooldownMinutes,
			"author_name":        ch.AuthorName,
		})
	}

	if challenges == nil {
		challenges = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"challenges": challenges, "total": len(challenges)})
}

// CreateChallenge creates a new challenge
func (h *AdminChallengeHandler) Create(c *gin.Context) {
	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate slug
	challengeSlug := slug.Make(req.Name)

	// Determine challenge type
	challengeType := req.ChallengeType
	if challengeType == "" {
		challengeType = "docker" // default
	}

	// Set defaults based on type
	resourceType := "docker"
	supportsDocker := true
	supportsVM := false

	if challengeType == "vm" {
		resourceType = "vm"
		supportsDocker = false
		supportsVM = true
		if req.VCPU == 0 {
			req.VCPU = 1
		}
		if req.MemoryMB == 0 {
			req.MemoryMB = 1024
		}
	} else {
		if req.ContainerTag == "" {
			req.ContainerTag = "latest"
		}
		if req.CPULimit == "" {
			req.CPULimit = "1"
		}
		if req.MemoryLimit == "" {
			req.MemoryLimit = "512m"
		}
	}

	if req.BasePoints == 0 {
		req.BasePoints = 100
	}

	// Resolve category name to ID (or create the category if new).
	// category_id takes precedence over category (name) when both are supplied.
	if req.Category != "" && req.CategoryID == nil {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), req.Category)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		req.CategoryID = catID
	}

	// Convert exposed ports to JSON
	portsJSON, _ := json.Marshal(req.ExposedPorts)

	// Start transaction
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	challengeID := uuid.New()

	// Insert challenge
	_, err = tx.Exec(c.Request.Context(),
		`INSERT INTO challenges (
			id, name, slug, description, difficulty, category_id, status,
			container_image, container_tag, container_platform, cpu_limit, memory_limit,
			exposed_ports, base_points, instance_timeout, max_extensions,
			vm_timeout_minutes, vm_max_extensions, vm_extension_minutes, cooldown_minutes,
			author_name, resource_type, supports_docker, supports_vm,
			total_flags, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, NOW(), NOW())`,
		challengeID, req.Name, challengeSlug, req.Description, req.Difficulty, req.CategoryID,
		req.ContainerImage, req.ContainerTag, req.ContainerPlatform, req.CPULimit, req.MemoryLimit,
		portsJSON, req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
		req.VMTimeoutMinutes, req.VMMaxExtensions, req.VMExtensionMinutes, req.CooldownMinutes,
		req.AuthorName, resourceType, supportsDocker, supportsVM, len(req.Flags),
	)
	if err != nil {
		h.logger.Error("failed to create challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge: " + err.Error()})
		return
	}

	// Handle flags - either multiple flags or single legacy flag
	flagsToCreate := req.Flags
	if len(flagsToCreate) == 0 && req.Flag != "" {
		// Legacy single flag support
		flagsToCreate = []FlagInput{{
			Name:      "Flag",
			Flag:      req.Flag,
			Points:    req.BasePoints,
			SortOrder: 1,
		}}
	}

	// Insert flags
	for i, flag := range flagsToCreate {
		flagID := uuid.New()
		sortOrder := flag.SortOrder
		if sortOrder == 0 {
			sortOrder = i + 1
		}
		flagType := flag.FlagType
		if flagType == "" {
			flagType = "static"
		}
		// Hash only for static flags; regex stores pattern as-is; dynamic has no pre-set value
		flagHash := ""
		if flagType == "static" {
			flagHash = hashFlag(flag.Flag)
		} else if flagType == "regex" {
			if _, err := regexp.Compile(flag.Flag); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regex pattern for flag '" + flag.Name + "': " + err.Error()})
				return
			}
			flagHash = flag.Flag
		}
		var dynPrefix *string
		if flag.DynamicFlagPrefix != "" {
			s := flag.DynamicFlagPrefix
			dynPrefix = &s
		}

		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO flags (id, challenge_id, name, description, flag_hash, points, sort_order,
			                    flag_type, dynamic_flag_prefix, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
			flagID, challengeID, flag.Name, flag.Description, flagHash, flag.Points, sortOrder,
			flagType, dynPrefix,
		)
		if err != nil {
			h.logger.Error("failed to create flag", zap.Error(err), zap.String("flag_name", flag.Name))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag: " + err.Error()})
			return
		}
	}

	// Update total_flags count
	_, err = tx.Exec(c.Request.Context(),
		`UPDATE challenges SET total_flags = $1 WHERE id = $2`,
		len(flagsToCreate), challengeID,
	)
	if err != nil {
		h.logger.Error("failed to update flag count", zap.Error(err))
	}

	// If VM challenge with template, create resource link
	if challengeType == "vm" && req.VMTemplateID != nil {
		resourceID := uuid.New()
		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO challenge_resources (id, challenge_id, resource_type, vm_template_id, cpu_limit, memory_limit, sort_order, is_active, created_at)
			 VALUES ($1, $2, 'vm', $3, $4, $5, 0, true, NOW())`,
			resourceID, challengeID, req.VMTemplateID,
			fmt.Sprintf("%d", req.VCPU), fmt.Sprintf("%dMB", req.MemoryMB),
		)
		if err != nil {
			h.logger.Error("failed to link VM template", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to link VM template"})
			return
		}
	}

	// Commit transaction
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logger.Error("failed to commit transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          challengeID.String(),
		"slug":        challengeSlug,
		"total_flags": len(flagsToCreate),
		"message":     "challenge created",
	})
}

// hashFlag creates a SHA256 hash of the flag
func hashFlag(flag string) string {
	hash := sha256.Sum256([]byte(flag))
	return hex.EncodeToString(hash[:])
}

// resolveOrCreateCategory looks up a category by name (case-insensitive) or creates it if absent.
// Returns nil when categoryName is blank.
func (h *AdminChallengeHandler) resolveOrCreateCategory(ctx context.Context, categoryName string) (*string, error) {
	trimmed := strings.TrimSpace(categoryName)
	if trimmed == "" {
		return nil, nil
	}

	var id string
	err := h.db.Pool.QueryRow(ctx,
		`SELECT id FROM categories WHERE LOWER(name) = LOWER($1)`, trimmed).Scan(&id)
	if err == nil {
		return &id, nil
	}

	// Category not found – create it
	newID := uuid.New().String()
	categorySlug := slug.Make(trimmed)
	_, err = h.db.Pool.Exec(ctx,
		`INSERT INTO categories (id, name, slug) VALUES ($1, $2, $3)`,
		newID, trimmed, categorySlug)
	if err != nil {
		// Another request may have created it concurrently; retry the lookup
		if retryErr := h.db.Pool.QueryRow(ctx,
			`SELECT id FROM categories WHERE LOWER(name) = LOWER($1)`, trimmed).Scan(&id); retryErr == nil {
			return &id, nil
		}
		return nil, fmt.Errorf("failed to create category: %w", err)
	}

	return &newID, nil
}

// CreateOVAChallenge handles OVA file upload and creates a VM challenge
func (h *AdminChallengeHandler) CreateOVAChallenge(c *gin.Context) {
	// Increase request timeout for large uploads
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 20<<30) // 20GB limit

	// Get form fields first (before file)
	name := c.PostForm("name")
	description := c.PostForm("description")
	difficulty := c.PostForm("difficulty")
	basePointsStr := c.PostForm("base_points")
	categoryName := c.PostForm("category")
	flagsJSON := c.PostForm("flags")

	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}
	if difficulty == "" {
		difficulty = "medium"
	}

	basePoints := 100
	if basePointsStr != "" {
		if bp, err := json.Number(basePointsStr).Int64(); err == nil {
			basePoints = int(bp)
		}
	}

	// Resolve category name to ID (or create the category if new)
	var categoryID *string
	if categoryName != "" {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), categoryName)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		categoryID = catID
	}

	// Parse flags
	var flags []FlagInput
	if flagsJSON != "" {
		if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid flags format: " + err.Error()})
			return
		}
	}

	// Get the uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.logger.Error("failed to get form file", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "OVA file is required: " + err.Error()})
		return
	}
	defer file.Close()

	h.logger.Info("received OVA file",
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	// Generate slug
	challengeSlug := slug.Make(name)
	challengeID := uuid.New()

	// Create temp directory if it doesn't exist
	tempDir := "/tmp/ova_uploads"
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		h.logger.Error("failed to create temp directory", zap.Error(err))
	}

	// Save file to disk
	tempPath := tempDir + "/" + challengeID.String() + "_" + header.Filename
	dst, err := os.Create(tempPath)
	if err != nil {
		h.logger.Error("failed to create temp file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		h.logger.Error("failed to write file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file: " + err.Error()})
		return
	}

	h.logger.Info("saved OVA file",
		zap.String("path", tempPath),
		zap.Int64("bytes", written),
	)

	// Start transaction
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// Insert challenge (include container_image as empty string to satisfy NOT NULL constraint)
	_, err = tx.Exec(c.Request.Context(),
		`INSERT INTO challenges (
			id, name, slug, description, difficulty, category_id, status,
			base_points, resource_type, supports_docker, supports_vm,
			total_flags, container_image, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, 'vm', false, true, $8, '', NOW(), NOW())`,
		challengeID, name, challengeSlug, description, difficulty, categoryID, basePoints, len(flags),
	)
	if err != nil {
		h.logger.Error("failed to create OVA challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge: " + err.Error()})
		return
	}

	// Insert flags
	for i, flag := range flags {
		flagID := uuid.New()
		flagHash := hashFlag(flag.Flag)
		sortOrder := i + 1

		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO flags (id, challenge_id, name, description, flag_hash, points, sort_order, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`,
			flagID, challengeID, flag.Name, flag.Description, flagHash, flag.Points, sortOrder,
		)
		if err != nil {
			h.logger.Error("failed to create flag", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create flag"})
			return
		}
	}

	// Commit transaction
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logger.Error("failed to commit transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge"})
		return
	}

	h.logger.Info("OVA challenge created",
		zap.String("challenge_id", challengeID.String()),
		zap.String("name", name),
		zap.String("file", header.Filename),
		zap.Int64("size", header.Size),
		zap.Int("flags", len(flags)),
	)

	c.JSON(http.StatusCreated, gin.H{
		"id":          challengeID.String(),
		"slug":        challengeSlug,
		"total_flags": len(flags),
		"file_path":   tempPath,
		"message":     "OVA challenge created. Processing in background.",
	})
}

// GetChallenge returns a challenge by ID
func (h *AdminChallengeHandler) Get(c *gin.Context) {
	challengeID := c.Param("id")

	query := `
		SELECT id, name, slug, description, difficulty, category_id, status,
		       container_image, container_tag, cpu_limit, memory_limit,
		       exposed_ports, base_points, instance_timeout, max_extensions,
		       author_name, total_solves, total_flags, created_at
		FROM challenges WHERE id = $1
	`

	var ch struct {
		ID              string
		Name            string
		Slug            string
		Description     *string
		Difficulty      string
		CategoryID      *string
		Status          string
		ContainerImage  string
		ContainerTag    string
		CPULimit        string
		MemoryLimit     string
		ExposedPorts    []byte
		BasePoints      int
		InstanceTimeout *int
		MaxExtensions   *int
		AuthorName      *string
		TotalSolves     int
		TotalFlags      int
		CreatedAt       time.Time
	}

	err := h.db.Pool.QueryRow(c.Request.Context(), query, challengeID).Scan(
		&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty, &ch.CategoryID, &ch.Status,
		&ch.ContainerImage, &ch.ContainerTag, &ch.CPULimit, &ch.MemoryLimit,
		&ch.ExposedPorts, &ch.BasePoints, &ch.InstanceTimeout, &ch.MaxExtensions,
		&ch.AuthorName, &ch.TotalSolves, &ch.TotalFlags, &ch.CreatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":               ch.ID,
		"name":             ch.Name,
		"slug":             ch.Slug,
		"description":      ch.Description,
		"difficulty":       ch.Difficulty,
		"category_id":      ch.CategoryID,
		"status":           ch.Status,
		"container_image":  ch.ContainerImage,
		"container_tag":    ch.ContainerTag,
		"cpu_limit":        ch.CPULimit,
		"memory_limit":     ch.MemoryLimit,
		"base_points":      ch.BasePoints,
		"instance_timeout": ch.InstanceTimeout,
		"max_extensions":   ch.MaxExtensions,
		"author_name":      ch.AuthorName,
		"total_solves":     ch.TotalSolves,
		"total_flags":      ch.TotalFlags,
		"created_at":       ch.CreatedAt.Unix(),
	})
}

// UpdateChallenge updates a challenge
func (h *AdminChallengeHandler) Update(c *gin.Context) {
	challengeID := c.Param("id")

	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve category name to ID (or create the category if new).
	// category_id takes precedence over category (name) when both are supplied.
	if req.Category != "" && req.CategoryID == nil {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), req.Category)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		req.CategoryID = catID
	}

	// Update challenge - handle both container and VM fields
	var err error
	if req.ResourceType != nil && *req.ResourceType == "vm" {
		// VM challenge - update VM-specific fields
		_, err = h.db.Pool.Exec(c.Request.Context(),
			`UPDATE challenges SET
				name = $1, description = $2, difficulty = $3, category_id = $4,
				base_points = $5, instance_timeout = $6, max_extensions = $7,
				cooldown_minutes = $8, author_name = $9, resource_type = $10, updated_at = NOW()
			WHERE id = $11`,
			req.Name, req.Description, req.Difficulty, req.CategoryID,
			req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
			req.CooldownMinutes, req.AuthorName, req.ResourceType, challengeID,
		)

		// Update VM template association if provided
		if req.VMTemplateID != nil {
			// First, remove old resource if exists
			_, _ = h.db.Pool.Exec(c.Request.Context(),
				`DELETE FROM challenge_resources WHERE challenge_id = $1`, challengeID)

			// Add new resource
			_, err = h.db.Pool.Exec(c.Request.Context(),
				`INSERT INTO challenge_resources (challenge_id, resource_type, vm_template_id, created_at, updated_at)
				 VALUES ($1, 'vm', $2, NOW(), NOW())`,
				challengeID, req.VMTemplateID,
			)
		}
	} else {
		// Docker challenge - update container fields
		portsJSON, marshalErr := json.Marshal(req.ExposedPorts)
		if marshalErr != nil {
			h.logger.Error("failed to marshal exposed ports", zap.Error(marshalErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to process exposed ports"})
			return
		}
		_, err = h.db.Pool.Exec(c.Request.Context(),
			`UPDATE challenges SET
				name = $1, description = $2, difficulty = $3, category_id = $4,
				container_image = $5, container_tag = $6, container_platform = $7,
				cpu_limit = $8, memory_limit = $9, exposed_ports = $10,
				base_points = $11, instance_timeout = $12, max_extensions = $13,
				cooldown_minutes = $14, author_name = $15, updated_at = NOW()
			WHERE id = $16`,
			req.Name, req.Description, req.Difficulty, req.CategoryID,
			req.ContainerImage, req.ContainerTag, req.ContainerPlatform,
			req.CPULimit, req.MemoryLimit, portsJSON,
			req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
			req.CooldownMinutes, req.AuthorName, challengeID,
		)
	}
	if err != nil {
		h.logger.Error("failed to update challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge updated"})
}

// DeleteChallenge deletes a challenge
func (h *AdminChallengeHandler) Delete(c *gin.Context) {
	challengeID := c.Param("id")

	// First, delete all instances for this challenge
	_, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM instances WHERE challenge_id = $1`, challengeID)
	if err != nil {
		h.logger.Error("failed to delete instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge instances"})
		return
	}

	// Then delete the challenge
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM challenges WHERE id = $1`, challengeID)
	if err != nil {
		h.logger.Error("failed to delete challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge deleted"})
}

// PublishChallenge publishes a challenge
func (h *AdminChallengeHandler) Publish(c *gin.Context) {
	challengeID := c.Param("id")

	// Check if challenge has at least one flag
	var flagCount int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM flags WHERE challenge_id = $1`, challengeID).Scan(&flagCount)

	if flagCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge must have at least one flag"})
		return
	}

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET status = 'published', release_date = NOW(), updated_at = NOW() WHERE id = $1`,
		challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge published"})
}

// UnpublishChallenge sets a challenge back to draft status
func (h *AdminChallengeHandler) Unpublish(c *gin.Context) {
	challengeID := c.Param("id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET status = 'draft', updated_at = NOW() WHERE id = $1`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unpublish challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge unpublished"})
}

// ArchiveChallenge archives a challenge
func (h *AdminChallengeHandler) Archive(c *gin.Context) {
	challengeID := c.Param("id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET status = 'archived', updated_at = NOW() WHERE id = $1`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge archived"})
}

// CreateFlagRequest represents the request to create a flag
type CreateFlagRequest struct {
	Name              string `json:"name" binding:"required"`
	Flag              string `json:"flag"` // empty when FlagType == "dynamic"
	Points            int    `json:"points"`
	Order             int    `json:"order"`
	CaseSensitive     bool   `json:"case_sensitive"`
	FlagType          string `json:"flag_type"`           // "static" | "dynamic"
	DynamicFlagPrefix string `json:"dynamic_flag_prefix"` // e.g. "H7CTF"
}

// ListFlags returns flags for a challenge
func (h *AdminChallengeHandler) ListFlags(c *gin.Context) {
	challengeID := c.Param("id")

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, flag_hash, points, sort_order, is_case_sensitive
		 FROM flags WHERE challenge_id = $1 ORDER BY sort_order`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
		return
	}
	defer rows.Close()

	var flags []gin.H
	for rows.Next() {
		var id, name, flag string
		var points, order int
		var caseSensitive bool
		if err := rows.Scan(&id, &name, &flag, &points, &order, &caseSensitive); err != nil {
			continue
		}
		flags = append(flags, gin.H{
			"id":             id,
			"name":           name,
			"flag":           flag,
			"points":         points,
			"order":          order,
			"case_sensitive": caseSensitive,
		})
	}

	if flags == nil {
		flags = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"flags": flags})
}

// CreateFlag creates a new flag
func (h *AdminChallengeHandler) CreateFlag(c *gin.Context) {
	challengeID := c.Param("id")

	var req CreateFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Points == 0 {
		req.Points = 100
	}

	flagType := req.FlagType
	if flagType == "" {
		flagType = "static"
	}
	flagHash := ""
	if flagType == "static" {
		flagHash = hashFlag(req.Flag)
	} else if flagType == "regex" {
		if _, err := regexp.Compile(req.Flag); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regex pattern: " + err.Error()})
			return
		}
		flagHash = req.Flag
	}
	var dynPrefix *string
	if req.DynamicFlagPrefix != "" {
		s := req.DynamicFlagPrefix
		dynPrefix = &s
	}

	flagID := uuid.New()
	_, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order, case_sensitive,
		                    flag_type, dynamic_flag_prefix, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
		flagID, challengeID, req.Name, flagHash, req.Points, req.Order, req.CaseSensitive,
		flagType, dynPrefix)
	if err != nil {
		h.logger.Error("failed to create flag", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		return
	}

	// Update flag count
	h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET total_flags = (SELECT COUNT(*) FROM flags WHERE challenge_id = $1) WHERE id = $1`,
		challengeID)

	c.JSON(http.StatusCreated, gin.H{"id": flagID.String(), "message": "flag created"})
}

// UpdateFlag updates a flag
func (h *AdminChallengeHandler) UpdateFlag(c *gin.Context) {
	flagID := c.Param("flag_id")

	var req CreateFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve flag_type
	if req.FlagType != "" {
		_, err := h.db.Pool.Exec(c.Request.Context(),
			`UPDATE flags SET flag_type = $1, dynamic_flag_prefix = NULLIF($2,'') WHERE id = $3`,
			req.FlagType, req.DynamicFlagPrefix, flagID)
		if err != nil {
			h.logger.Warn("failed to update flag_type", zap.Error(err))
		}
	}

	// Only update flag_hash if a new flag value is provided
	if req.Flag != "" {
		var flagHash string
		if req.FlagType == "regex" {
			if _, err := regexp.Compile(req.Flag); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regex pattern: " + err.Error()})
				return
			}
			flagHash = req.Flag
		} else {
			flagHash = hashFlag(req.Flag)
		}
		_, err := h.db.Pool.Exec(c.Request.Context(),
			`UPDATE flags SET name = $1, flag_hash = $2, points = $3, sort_order = $4, case_sensitive = $5, updated_at = NOW()
			 WHERE id = $6`,
			req.Name, flagHash, req.Points, req.Order, req.CaseSensitive, flagID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
			return
		}
	} else {
		_, err := h.db.Pool.Exec(c.Request.Context(),
			`UPDATE flags SET name = $1, points = $2, sort_order = $3, case_sensitive = $4, updated_at = NOW()
			 WHERE id = $5`,
			req.Name, req.Points, req.Order, req.CaseSensitive, flagID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "flag updated"})
}

// DeleteFlag deletes a flag
func (h *AdminChallengeHandler) DeleteFlag(c *gin.Context) {
	challengeID := c.Param("id")
	flagID := c.Param("flag_id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM flags WHERE id = $1`, flagID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		return
	}

	// Update flag count
	h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET total_flags = (SELECT COUNT(*) FROM flags WHERE challenge_id = $1) WHERE id = $1`,
		challengeID)

	c.JSON(http.StatusOK, gin.H{"message": "flag deleted"})
}

// ListHints lists all hints for a challenge
func (h *AdminChallengeHandler) ListHints(c *gin.Context) {
	challengeID := c.Param("id")

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, challenge_id, cost, content, created_at, updated_at 
		 FROM hints WHERE challenge_id = $1 ORDER BY cost ASC`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
	}
	defer rows.Close()

	var hints []map[string]interface{}
	for rows.Next() {
		var hint struct {
			ID          string
			ChallengeID string
			Cost        int
			Content     string
			CreatedAt   string
			UpdatedAt   string
		}
		if err := rows.Scan(&hint.ID, &hint.ChallengeID, &hint.Cost, &hint.Content, &hint.CreatedAt, &hint.UpdatedAt); err != nil {
			continue
		}
		hints = append(hints, map[string]interface{}{
			"id":           hint.ID,
			"challenge_id": hint.ChallengeID,
			"cost":         hint.Cost,
			"content":      hint.Content,
			"created_at":   hint.CreatedAt,
			"updated_at":   hint.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, hints)
}

// CreateHint creates a new hint for a challenge
func (h *AdminChallengeHandler) CreateHint(c *gin.Context) {
	challengeID := c.Param("id")

	var req struct {
		Cost    int    `json:"cost" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var hintID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO hints (challenge_id, cost, content) VALUES ($1, $2, $3) RETURNING id`,
		challengeID, req.Cost, req.Content).Scan(&hintID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create hint"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": hintID, "message": "hint created"})
}

// UpdateHint updates a hint
func (h *AdminChallengeHandler) UpdateHint(c *gin.Context) {
	hintID := c.Param("hint_id")

	var req struct {
		Cost    int    `json:"cost"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE hints SET cost = $1, content = $2, updated_at = NOW() WHERE id = $3`,
		req.Cost, req.Content, hintID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update hint"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "hint updated"})
}

// DeleteHint deletes a hint
func (h *AdminChallengeHandler) DeleteHint(c *gin.Context) {
	hintID := c.Param("hint_id")

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM hints WHERE id = $1`, hintID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete hint"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "hint deleted"})
}

// ListFlagShareEvents returns all detected flag-sharing events for admin review.
// GET /api/v1/admin/flag-shares[?challenge_id=&submitter_id=&limit=]
func (h *AdminChallengeHandler) ListFlagShareEvents(c *gin.Context) {
	challengeIDFilter := c.Query("challenge_id")
	submitterFilter := c.Query("submitter_id")
	limit := 500

	var cid, sid interface{} = nil, nil
	if challengeIDFilter != "" {
		cid = challengeIDFilter
	}
	if submitterFilter != "" {
		sid = submitterFilter
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT
			fse.id,
			fse.created_at,
			fse.challenge_id,
			ch.name        AS challenge_name,
			fse.flag_id,
			f.name         AS flag_name,
			fse.flag_value,
			fse.owner_user_id,
			owner.username AS owner_username,
			fse.submitter_user_id,
			sub.username   AS submitter_username,
			fse.submitter_ip,
			fse.owner_instance_id
		FROM flag_share_events fse
		JOIN challenges ch  ON ch.id  = fse.challenge_id
		JOIN flags      f   ON f.id   = fse.flag_id
		JOIN users      owner ON owner.id = fse.owner_user_id
		JOIN users      sub   ON sub.id   = fse.submitter_user_id
		WHERE ($1::uuid IS NULL OR fse.challenge_id      = $1)
		  AND ($2::uuid IS NULL OR fse.submitter_user_id = $2)
		ORDER BY fse.created_at DESC
		LIMIT $3`,
		cid, sid, limit)
	if err != nil {
		h.logger.Error("failed to list flag share events", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flag share events"})
		return
	}
	defer rows.Close()

	type shareRow struct {
		ID                string `json:"id"`
		CreatedAt         int64  `json:"created_at"`
		ChallengeID       string `json:"challenge_id"`
		ChallengeName     string `json:"challenge_name"`
		FlagID            string `json:"flag_id"`
		FlagName          string `json:"flag_name"`
		FlagValue         string `json:"flag_value"`
		OwnerUserID       string `json:"owner_user_id"`
		OwnerUsername     string `json:"owner_username"`
		SubmitterUserID   string `json:"submitter_user_id"`
		SubmitterUsername string `json:"submitter_username"`
		SubmitterIP       string `json:"submitter_ip"`
		OwnerInstanceID   string `json:"owner_instance_id"`
	}

	var results []shareRow
	for rows.Next() {
		var r shareRow
		var createdAt time.Time
		if err := rows.Scan(
			&r.ID, &createdAt, &r.ChallengeID, &r.ChallengeName,
			&r.FlagID, &r.FlagName, &r.FlagValue,
			&r.OwnerUserID, &r.OwnerUsername,
			&r.SubmitterUserID, &r.SubmitterUsername,
			&r.SubmitterIP, &r.OwnerInstanceID,
		); err != nil {
			continue
		}
		r.CreatedAt = createdAt.Unix()
		results = append(results, r)
	}
	if results == nil {
		results = []shareRow{}
	}

	c.JSON(http.StatusOK, gin.H{"flag_shares": results, "total": len(results)})
}

// ListInstanceFlags returns all generated dynamic flags — admin monitoring only.
// GET /api/v1/admin/instance-flags[?challenge_id=&user_id=&limit=]
func (h *AdminChallengeHandler) ListInstanceFlags(c *gin.Context) {
	challengeIDFilter := c.Query("challenge_id")
	userIDFilter := c.Query("user_id")
	limit := 500

	query := `
		SELECT
			inf.id,
			inf.instance_id,
			inf.user_id,
			u.username,
			inf.challenge_id,
			ch.name  AS challenge_name,
			inf.flag_id,
			f.name   AS flag_name,
			f.flag_type,
			inf.flag_value,
			inf.created_at,
			i.status AS instance_status
		FROM instance_flags inf
		JOIN users      u  ON u.id  = inf.user_id
		JOIN challenges ch ON ch.id = inf.challenge_id
		JOIN flags      f  ON f.id  = inf.flag_id
		JOIN instances  i  ON i.id  = inf.instance_id
		WHERE ($1::uuid IS NULL OR inf.challenge_id = $1)
		  AND ($2::uuid IS NULL OR inf.user_id      = $2)
		ORDER BY inf.created_at DESC
		LIMIT $3
	`

	var cid, uid interface{} = nil, nil
	if challengeIDFilter != "" {
		cid = challengeIDFilter
	}
	if userIDFilter != "" {
		uid = userIDFilter
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), query, cid, uid, limit)
	if err != nil {
		h.logger.Error("failed to list instance flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instance flags"})
		return
	}
	defer rows.Close()

	type row struct {
		ID             string `json:"id"`
		InstanceID     string `json:"instance_id"`
		UserID         string `json:"user_id"`
		Username       string `json:"username"`
		ChallengeID    string `json:"challenge_id"`
		ChallengeName  string `json:"challenge_name"`
		FlagID         string `json:"flag_id"`
		FlagName       string `json:"flag_name"`
		FlagType       string `json:"flag_type"`
		FlagValue      string `json:"flag_value"`
		CreatedAt      int64  `json:"created_at"`
		InstanceStatus string `json:"instance_status"`
	}

	var results []row
	for rows.Next() {
		var r row
		var createdAt time.Time
		if err := rows.Scan(
			&r.ID, &r.InstanceID, &r.UserID, &r.Username,
			&r.ChallengeID, &r.ChallengeName,
			&r.FlagID, &r.FlagName, &r.FlagType,
			&r.FlagValue, &createdAt, &r.InstanceStatus,
		); err != nil {
			continue
		}
		r.CreatedAt = createdAt.Unix()
		results = append(results, r)
	}
	if results == nil {
		results = []row{}
	}

	c.JSON(http.StatusOK, gin.H{"instance_flags": results, "total": len(results)})
}

// GetStats returns platform statistics
func (h *StatsHandler) Get(c *gin.Context) {
	// Single optimized query for all stats
	query := `
		SELECT
			(SELECT COUNT(*) FROM users WHERE role != 'admin') as total_users,
			(SELECT COUNT(*) FROM challenges) as total_challenges,
			(SELECT COUNT(*) FROM challenges WHERE status = 'published') as published_challenges,
			(SELECT COUNT(*) FROM challenges WHERE status = 'draft') as draft_challenges,
			(SELECT COUNT(*) FROM solves) as total_solves,
			(SELECT COUNT(*) FROM instances) as total_instances,
			(SELECT COUNT(*) FROM instances WHERE status = 'running') as active_instances
	`

	var stats struct {
		TotalUsers          int
		TotalChallenges     int
		PublishedChallenges int
		DraftChallenges     int
		TotalSolves         int
		TotalInstances      int
		ActiveInstances     int
	}

	err := h.db.Pool.QueryRow(c.Request.Context(), query).Scan(
		&stats.TotalUsers,
		&stats.TotalChallenges,
		&stats.PublishedChallenges,
		&stats.DraftChallenges,
		&stats.TotalSolves,
		&stats.TotalInstances,
		&stats.ActiveInstances,
	)

	if err != nil {
		h.logger.Error("failed to fetch stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_users":          stats.TotalUsers,
		"total_challenges":     stats.TotalChallenges,
		"published_challenges": stats.PublishedChallenges,
		"draft_challenges":     stats.DraftChallenges,
		"total_solves":         stats.TotalSolves,
		"total_instances":      stats.TotalInstances,
		"active_instances":     stats.ActiveInstances,
	})
}
