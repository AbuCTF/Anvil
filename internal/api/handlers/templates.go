package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// VMTemplateHandler handles VM template management
type VMTemplateHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

// NewVMTemplateHandler creates a new template handler
func NewVMTemplateHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *VMTemplateHandler {
	return &VMTemplateHandler{config: cfg, db: db, logger: logger}
}

// TemplateResponse represents a VM template in API responses
type TemplateResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Slug           string            `json:"slug"`
	Description    string            `json:"description"`
	ImagePath      string            `json:"image_path"`
	OriginalFormat string            `json:"original_format"`
	ImageSizeBytes int64             `json:"image_size_bytes"`
	DiskGB         int               `json:"disk_gb"`
	VCPU           int               `json:"vcpu"`
	MemoryMB       int               `json:"memory_mb"`
	OSType         string            `json:"os_type"`
	OSVariant      string            `json:"os_variant,omitempty"`
	OSName         string            `json:"os_name,omitempty"`
	NetworkMode    string            `json:"network_mode"`
	IsActive       bool              `json:"is_active"`
	IsPublic       bool              `json:"is_public"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CreatedAt      int64             `json:"created_at"`
}

// List returns all VM templates
// GET /api/v1/admin/vm-templates
func (h *VMTemplateHandler) List(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, name, slug, description, image_path, original_format::text, image_size,
		       disk_gb, vcpu, memory_mb, os_type, os_variant, os_name, network_mode,
		       is_active, is_public, created_at
		FROM vm_templates
		ORDER BY created_at DESC
	`)
	if err != nil {
		h.logger.Error("failed to list templates", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch templates"})
		return
	}
	defer rows.Close()

	var templates []TemplateResponse
	for rows.Next() {
		var t TemplateResponse
		var description, osVariant, osName *string
		var createdAt time.Time

		if err := rows.Scan(
			&t.ID, &t.Name, &t.Slug, &description, &t.ImagePath, &t.OriginalFormat,
			&t.ImageSizeBytes, &t.DiskGB, &t.VCPU, &t.MemoryMB, &t.OSType,
			&osVariant, &osName, &t.NetworkMode, &t.IsActive, &t.IsPublic, &createdAt,
		); err != nil {
			h.logger.Error("failed to scan template", zap.Error(err))
			continue
		}

		if description != nil {
			t.Description = *description
		}
		if osVariant != nil {
			t.OSVariant = *osVariant
		}
		if osName != nil {
			t.OSName = *osName
		}
		t.CreatedAt = createdAt.Unix()
		templates = append(templates, t)
	}

	if templates == nil {
		templates = []TemplateResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"templates": templates,
		"total":     len(templates),
	})
}

// Get returns a specific template
// GET /api/v1/admin/vm-templates/:id
func (h *VMTemplateHandler) Get(c *gin.Context) {
	templateID := c.Param("id")

	var t TemplateResponse
	var description, osVariant, osName *string
	var createdAt time.Time

	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT id, name, slug, description, image_path, original_format::text, image_size,
		       disk_gb, vcpu, memory_mb, os_type, os_variant, os_name, network_mode,
		       is_active, is_public, created_at
		FROM vm_templates WHERE id = $1
	`, templateID).Scan(
		&t.ID, &t.Name, &t.Slug, &description, &t.ImagePath, &t.OriginalFormat,
		&t.ImageSizeBytes, &t.DiskGB, &t.VCPU, &t.MemoryMB, &t.OSType,
		&osVariant, &osName, &t.NetworkMode, &t.IsActive, &t.IsPublic, &createdAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	if description != nil {
		t.Description = *description
	}
	if osVariant != nil {
		t.OSVariant = *osVariant
	}
	if osName != nil {
		t.OSName = *osName
	}
	t.CreatedAt = createdAt.Unix()

	c.JSON(http.StatusOK, t)
}

// UploadProgress tracks OVA upload and conversion progress
type UploadProgress struct {
	UploadID string `json:"upload_id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Upload handles OVA file upload with chunked transfer
// POST /api/v1/admin/vm-templates/upload
func (h *VMTemplateHandler) Upload(c *gin.Context) {
	// Get form values
	name := c.PostForm("name")
	description := c.PostForm("description")
	minVCPU := c.DefaultPostForm("min_vcpu", "2")
	minMemoryMB := c.DefaultPostForm("min_memory_mb", "2048")
	osType := c.DefaultPostForm("os_type", "linux")

	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	// Get the uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.logger.Error("failed to get file", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file provided"})
		return
	}
	defer file.Close()

	originalName := header.Filename
	ext := strings.ToLower(filepath.Ext(originalName))

	if ext != ".ova" && ext != ".qcow2" && ext != ".vmdk" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file format. Use .ova, .qcow2, or .vmdk"})
		return
	}

	// Generate IDs
	uploadID := uuid.New()
	templateID := uuid.New()

	// Create upload record in database
	_, err = h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO uploads (id, user_id, filename, size_bytes, content_type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'uploading', NOW(), NOW())
	`, uploadID, c.GetString("user_id"), originalName, header.Size, header.Header.Get("Content-Type"))
	if err != nil {
		h.logger.Error("failed to create upload record", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initiate upload"})
		return
	}

	// Ensure directories exist
	basePath := "/var/lib/anvil/images"
	uploadsDir := filepath.Join(basePath, "uploads")
	templatesDir := filepath.Join(basePath, "templates")

	os.MkdirAll(uploadsDir, 0755)
	os.MkdirAll(templatesDir, 0755)

	// Save uploaded file
	uploadPath := filepath.Join(uploadsDir, fmt.Sprintf("%s%s", uploadID.String(), ext))
	outFile, err := os.Create(uploadPath)
	if err != nil {
		h.logger.Error("failed to create upload file", zap.Error(err))
		h.updateUploadStatus(c, uploadID.String(), "failed", "failed to create file")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	// Copy with hash calculation
	hasher := sha256.New()
	writer := io.MultiWriter(outFile, hasher)

	written, err := io.Copy(writer, file)
	outFile.Close()

	if err != nil {
		h.logger.Error("failed to save file", zap.Error(err))
		os.Remove(uploadPath)
		h.updateUploadStatus(c, uploadID.String(), "failed", "upload interrupted")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "upload failed"})
		return
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))

	h.logger.Info("file uploaded",
		zap.String("upload_id", uploadID.String()),
		zap.String("filename", originalName),
		zap.Int64("size", written),
		zap.String("checksum", checksum))

	// Update upload status
	h.updateUploadStatus(c, uploadID.String(), "processing", "converting to qcow2")

	// Convert to QCOW2 (async in background)
	go h.processUpload(uploadID.String(), templateID.String(), name, description, originalName, uploadPath, templatesDir, checksum, minVCPU, minMemoryMB, osType)

	c.JSON(http.StatusAccepted, gin.H{
		"upload_id":   uploadID.String(),
		"template_id": templateID.String(),
		"message":     "upload received, processing in background",
		"status":      "processing",
	})
}

func (h *VMTemplateHandler) updateUploadStatus(c *gin.Context, uploadID, status, message string) {
	h.db.Pool.Exec(c.Request.Context(), `
		UPDATE uploads SET status = $1, updated_at = NOW() WHERE id = $2
	`, status, uploadID)
}

func (h *VMTemplateHandler) processUpload(uploadID, templateID, name, description, originalName, uploadPath, templatesDir, checksum, minVCPU, minMemoryMB, osType string) {
	ctx := context.Background()

	// Determine output path
	safeName := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	safeName = strings.ReplaceAll(safeName, "_", "-")
	qcow2Path := filepath.Join(templatesDir, fmt.Sprintf("%s.qcow2", safeName))

	ext := strings.ToLower(filepath.Ext(uploadPath))
	var diskSizeGB float64

	if ext == ".qcow2" {
		// Already QCOW2, just move it
		if err := os.Rename(uploadPath, qcow2Path); err != nil {
			// Copy if rename fails (cross-device)
			src, _ := os.Open(uploadPath)
			dst, err := os.Create(qcow2Path)
			if err != nil {
				h.logger.Error("failed to create qcow2 file", zap.Error(err))
				h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'failed' WHERE id = $1`, uploadID)
				return
			}
			io.Copy(dst, src)
			src.Close()
			dst.Close()
			os.Remove(uploadPath)
		}
	} else if ext == ".ova" || ext == ".vmdk" {
		// Convert using qemu-img
		var inputPath string

		if ext == ".ova" {
			// Extract OVA (it's a tar file)
			extractDir := filepath.Join(filepath.Dir(uploadPath), uploadID+"-extracted")
			os.MkdirAll(extractDir, 0755)
			defer os.RemoveAll(extractDir)

			cmd := exec.Command("tar", "-xf", uploadPath, "-C", extractDir)
			if err := cmd.Run(); err != nil {
				h.logger.Error("failed to extract OVA", zap.Error(err))
				h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'failed' WHERE id = $1`, uploadID)
				os.Remove(uploadPath)
				return
			}

			// Find the VMDK file
			files, _ := filepath.Glob(filepath.Join(extractDir, "*.vmdk"))
			if len(files) == 0 {
				h.logger.Error("no VMDK found in OVA")
				h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'failed' WHERE id = $1`, uploadID)
				os.Remove(uploadPath)
				return
			}
			inputPath = files[0]
		} else {
			inputPath = uploadPath
		}

		// Convert to QCOW2
		h.logger.Info("converting to qcow2", zap.String("input", inputPath), zap.String("output", qcow2Path))
		cmd := exec.Command("qemu-img", "convert", "-f", "vmdk", "-O", "qcow2", inputPath, qcow2Path)
		output, err := cmd.CombinedOutput()
		if err != nil {
			h.logger.Error("qemu-img convert failed", zap.Error(err), zap.String("output", string(output)))
			h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'failed' WHERE id = $1`, uploadID)
			os.Remove(uploadPath)
			return
		}

		// Clean up original
		os.Remove(uploadPath)
	}

	// Get disk size
	info, err := os.Stat(qcow2Path)
	var imageSizeBytes int64
	if err == nil {
		imageSizeBytes = info.Size()
		diskSizeGB = float64(imageSizeBytes) / (1024 * 1024 * 1024)
	}

	// Generate slug from name
	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	slug = strings.ReplaceAll(slug, "_", "-")

	// Determine original format
	originalFormat := "qcow2"
	if strings.HasSuffix(strings.ToLower(originalName), ".ova") {
		originalFormat = "ova"
	} else if strings.HasSuffix(strings.ToLower(originalName), ".vmdk") {
		originalFormat = "vmdk"
	}

	// Parse vcpu and memory
	vcpuInt := 2
	memoryInt := 2048
	fmt.Sscanf(minVCPU, "%d", &vcpuInt)
	fmt.Sscanf(minMemoryMB, "%d", &memoryInt)

	// Create template record with correct column names
	_, err = h.db.Pool.Exec(ctx, `
		INSERT INTO vm_templates (
			id, upload_id, name, slug, description, image_path, original_format, original_path,
			image_size, vcpu, memory_mb, disk_gb, os_type, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::vm_image_format, $8, $9, $10, $11, $12, $13, true, NOW(), NOW())
	`, templateID, uploadID, name, slug, description, qcow2Path, originalFormat, uploadPath,
		imageSizeBytes, vcpuInt, memoryInt, int(diskSizeGB)+1, osType)

	if err != nil {
		h.logger.Error("failed to create template record", zap.Error(err))
		h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'failed' WHERE id = $1`, uploadID)
		return
	}

	// Update upload status
	h.db.Pool.Exec(ctx, `UPDATE uploads SET status = 'completed' WHERE id = $1`, uploadID)

	h.logger.Info("template created",
		zap.String("template_id", templateID),
		zap.String("name", name),
		zap.Float64("disk_gb", diskSizeGB))
}

// GetUploadStatus returns the status of an upload
// GET /api/v1/admin/vm-templates/upload/:id/status
func (h *VMTemplateHandler) GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("id")

	var status, filename string
	var sizeBytes int64
	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT status, filename, size_bytes FROM uploads WHERE id = $1
	`, uploadID).Scan(&status, &filename, &sizeBytes)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	// Check if template was created
	var templateID *string
	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT id FROM vm_templates WHERE original_name = $1
		ORDER BY created_at DESC LIMIT 1
	`, filename).Scan(&templateID)

	response := gin.H{
		"upload_id": uploadID,
		"status":    status,
		"filename":  filename,
		"size":      sizeBytes,
	}

	if templateID != nil {
		response["template_id"] = *templateID
	}

	c.JSON(http.StatusOK, response)
}

// Delete removes a VM template
// DELETE /api/v1/admin/vm-templates/:id
func (h *VMTemplateHandler) Delete(c *gin.Context) {
	templateID := c.Param("id")

	// Get image path
	var imagePath string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT image_path FROM vm_templates WHERE id = $1`, templateID).Scan(&imagePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	// Check if any challenges use this template
	var challengeCount int
	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM challenge_resources 
		WHERE resource_type = 'vm_template' AND resource_reference = $1
	`, templateID).Scan(&challengeCount)

	if challengeCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "template is used by challenges",
			"challenges": challengeCount,
		})
		return
	}

	// Delete template record
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM vm_templates WHERE id = $1`, templateID)
	if err != nil {
		h.logger.Error("failed to delete template", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	// Remove file (optional - could keep for recovery)
	if imagePath != "" {
		os.Remove(imagePath)
	}

	c.JSON(http.StatusOK, gin.H{"message": "template deleted"})
}

// Update modifies a VM template
// PUT /api/v1/admin/vm-templates/:id
func (h *VMTemplateHandler) Update(c *gin.Context) {
	templateID := c.Param("id")

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		VCPU        *int    `json:"vcpu"`
		MemoryMB    *int    `json:"memory_mb"`
		IsActive    *bool   `json:"is_active"`
		IsPublic    *bool   `json:"is_public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE vm_templates SET
			name = COALESCE($1, name),
			description = COALESCE($2, description),
			vcpu = COALESCE($3, vcpu),
			memory_mb = COALESCE($4, memory_mb),
			is_active = COALESCE($5, is_active),
			is_public = COALESCE($6, is_public),
			updated_at = NOW()
		WHERE id = $7
	`, req.Name, req.Description, req.VCPU, req.MemoryMB, req.IsActive, req.IsPublic, templateID)

	if err != nil {
		h.logger.Error("failed to update template", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update template"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "template updated"})
}

// TemplateRegisterRequest represents a request to register an existing QCOW2 file
type TemplateRegisterRequest struct {
	Name        string `json:"name" binding:"required"`
	ImagePath   string `json:"image_path" binding:"required"`
	Description string `json:"description"`
	DiskGB      int    `json:"disk_gb" binding:"required"`
	VCPU        int    `json:"vcpu"`
	MemoryMB    int    `json:"memory_mb"`
	OSType      string `json:"os_type"`
	OSVariant   string `json:"os_variant"`
	OSName      string `json:"os_name"`
	NetworkMode string `json:"network_mode"`
}

// Register adds an existing QCOW2 file as a template
// POST /api/v1/admin/vm-templates/register
func (h *VMTemplateHandler) Register(c *gin.Context) {
	var req TemplateRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate file exists
	info, err := os.Stat(req.ImagePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file not found: " + req.ImagePath})
		return
	}

	// Set defaults
	if req.VCPU == 0 {
		req.VCPU = 2
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 2048
	}
	if req.OSType == "" {
		req.OSType = "linux"
	}
	if req.NetworkMode == "" {
		req.NetworkMode = "nat"
	}

	// Generate slug from name
	slug := strings.ToLower(strings.ReplaceAll(req.Name, " ", "-"))
	slug = strings.ReplaceAll(slug, "_", "-")

	templateID := uuid.New()

	_, err = h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO vm_templates (
			id, name, slug, description, image_path, original_format, image_size,
			disk_gb, vcpu, memory_mb, os_type, os_variant, os_name, network_mode,
			is_active, is_public, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'qcow2', $6, $7, $8, $9, $10, $11, $12, $13, true, false, NOW(), NOW())
	`, templateID, req.Name, slug, req.Description, req.ImagePath, info.Size(),
		req.DiskGB, req.VCPU, req.MemoryMB, req.OSType, req.OSVariant, req.OSName, req.NetworkMode)

	if err != nil {
		h.logger.Error("failed to register template", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register template: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":      templateID.String(),
		"message": "template registered",
	})
}

// ListActiveInstances returns all running VM instances
// GET /api/v1/admin/vm-instances
func (h *VMTemplateHandler) ListActiveInstances(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT i.id, i.user_id, u.username, i.challenge_id, ch.name,
		       i.name, i.status::text, i.ip_address, i.created_at, i.expires_at
		FROM vm_instances i
		JOIN users u ON i.user_id = u.id
		JOIN challenges ch ON i.challenge_id = ch.id
		WHERE i.status IN ('running', 'starting', 'stopping')
		ORDER BY i.created_at DESC
	`)
	if err != nil {
		h.logger.Error("failed to list instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}
	defer rows.Close()

	type InstanceResponse struct {
		ID            string  `json:"id"`
		UserID        string  `json:"user_id"`
		Username      string  `json:"username"`
		ChallengeID   string  `json:"challenge_id"`
		ChallengeName string  `json:"challenge_name"`
		VMName        string  `json:"vm_name"`
		Status        string  `json:"status"`
		IPAddress     *string `json:"ip_address"`
		CreatedAt     int64   `json:"created_at"`
		ExpiresAt     *int64  `json:"expires_at"`
	}

	var instances []InstanceResponse
	for rows.Next() {
		var inst InstanceResponse
		var createdAt time.Time
		var expiresAt *time.Time

		if err := rows.Scan(
			&inst.ID, &inst.UserID, &inst.Username, &inst.ChallengeID, &inst.ChallengeName,
			&inst.VMName, &inst.Status, &inst.IPAddress, &createdAt, &expiresAt,
		); err != nil {
			continue
		}

		inst.CreatedAt = createdAt.Unix()
		if expiresAt != nil {
			ts := expiresAt.Unix()
			inst.ExpiresAt = &ts
		}
		instances = append(instances, inst)
	}

	if instances == nil {
		instances = []InstanceResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"total":     len(instances),
	})
}
