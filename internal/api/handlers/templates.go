package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/vmimage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// VMTemplateHandler handles VM template management
type VMTemplateHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

const (
	vmImageBasePath             = "/var/lib/anvil/images"
	maxVMTemplateUploadBytes    = int64(50 * 1024 * 1024 * 1024)
	maxVMTemplateRequestBytes   = maxVMTemplateUploadBytes + 64*1024*1024
	vmTemplateMultipartMemory   = int64(32 * 1024 * 1024)
	vmTemplateConversionTimeout = 4 * time.Hour
	vmTemplateInspectionTimeout = 30 * time.Second
	maxVMConversionOutputBytes  = 64 * 1024
)

// NewVMTemplateHandler creates a new template handler
func NewVMTemplateHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *VMTemplateHandler {
	return &VMTemplateHandler{config: cfg, db: db, logger: logger}
}

func (h *VMTemplateHandler) ready(c *gin.Context) bool {
	if h == nil || h.db == nil || h.db.Pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VM template service unavailable"})
		return false
	}
	return true
}

func (h *VMTemplateHandler) safeLogger() *zap.Logger {
	if h != nil && h.logger != nil {
		return h.logger
	}
	return zap.NewNop()
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
	if !h.ready(c) {
		return
	}
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, name, slug, description, image_path, original_format::text, image_size,
		       disk_gb, vcpu, memory_mb, COALESCE(os_type, ''), os_variant, os_name,
		       COALESCE(network_mode, 'nat'), COALESCE(is_active, false),
		       COALESCE(is_public, false), COALESCE(created_at, to_timestamp(0))
		FROM vm_templates
		ORDER BY created_at DESC
	`)
	if err != nil {
		h.safeLogger().Error("failed to list templates", zap.Error(err))
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
			h.safeLogger().Error("failed to scan template", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch templates"})
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
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		h.safeLogger().Error("failed while listing templates", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch templates"})
		return
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
	if !h.ready(c) {
		return
	}
	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	var t TemplateResponse
	var description, osVariant, osName *string
	var createdAt time.Time

	err = h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT id, name, slug, description, image_path, original_format::text, image_size,
		       disk_gb, vcpu, memory_mb, COALESCE(os_type, ''), os_variant, os_name,
		       COALESCE(network_mode, 'nat'), COALESCE(is_active, false),
		       COALESCE(is_public, false), COALESCE(created_at, to_timestamp(0))
		FROM vm_templates WHERE id = $1
	`, templateID).Scan(
		&t.ID, &t.Name, &t.Slug, &description, &t.ImagePath, &t.OriginalFormat,
		&t.ImageSizeBytes, &t.DiskGB, &t.VCPU, &t.MemoryMB, &t.OSType,
		&osVariant, &osName, &t.NetworkMode, &t.IsActive, &t.IsPublic, &createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		h.safeLogger().Error("failed to fetch template", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch template"})
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
	if !h.ready(c) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVMTemplateRequestBytes)
	if err := c.Request.ParseMultipartForm(vmTemplateMultipartMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "VM image exceeds the 50 GB upload limit"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart upload"})
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() {
			if err := c.Request.MultipartForm.RemoveAll(); err != nil {
				h.safeLogger().Warn("failed to clean multipart temporary files", zap.Error(err))
			}
		}()
	}

	// Get form values
	name := strings.TrimSpace(c.PostForm("name"))
	description := c.PostForm("description")
	osType := strings.TrimSpace(c.DefaultPostForm("os_type", "linux"))
	if osType == "" {
		osType = "linux"
	}
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if len(name) > 200 || len(osType) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name or os_type exceeds its maximum length"})
		return
	}
	templateSlug := slug.Make(name)
	if templateSlug == "" || len(templateSlug) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain URL-safe letters or numbers"})
		return
	}
	minVCPU, err := parsePositiveTemplateResource(c.DefaultPostForm("min_vcpu", "1"), "min_vcpu")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	minMemoryMB, err := parsePositiveTemplateResource(c.DefaultPostForm("min_memory_mb", "1024"), "min_memory_mb")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get the uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file provided"})
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.safeLogger().Warn("failed to close uploaded template stream", zap.Error(err))
		}
	}()
	if header.Size < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VM image must not be empty"})
		return
	}
	if header.Size > maxVMTemplateUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "VM image exceeds the 50 GB upload limit"})
		return
	}

	originalName := sanitiseFilename(header.Filename)
	if len(originalName) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename exceeds 500 characters"})
		return
	}
	ext := strings.ToLower(filepath.Ext(originalName))

	if ext != ".ova" && ext != ".qcow2" && ext != ".vmdk" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file format. Use .ova, .qcow2, or .vmdk"})
		return
	}

	// Generate IDs
	uploadID := uuid.New()
	templateID := uuid.New()

	// Get user ID from context
	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
		return
	}

	// Create upload record in database
	result, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO uploads (id, user_id, filename, total_size, content_type, file_type, chunk_size, total_chunks, storage_key, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'vm_template', 1, 1, $6, 'uploading', NOW(), NOW())
	`, uploadID, *userID, originalName, header.Size, header.Header.Get("Content-Type"), uploadID.String())
	if err != nil {
		h.safeLogger().Error("failed to create upload record", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initiate upload"})
		return
	}
	if result.RowsAffected() != 1 {
		h.safeLogger().Error("upload insert affected an unexpected number of rows", zap.String("upload_id", uploadID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initiate upload"})
		return
	}

	// Ensure directories exist
	basePath := vmImageBasePath
	uploadsDir := filepath.Join(basePath, "uploads")
	templatesDir := filepath.Join(basePath, "templates")

	for _, dir := range []string{uploadsDir, templatesDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			dirErr := fmt.Errorf("create VM image directory: %w", err)
			h.safeLogger().Error("failed to prepare VM image directories", zap.String("directory", dir), zap.Error(err))
			h.markUploadFailed(c.Request.Context(), uploadID.String(), dirErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare image storage"})
			return
		}
	}

	// Save uploaded file
	uploadPath := filepath.Join(uploadsDir, fmt.Sprintf("%s%s", uploadID.String(), ext))
	outFile, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		h.safeLogger().Error("failed to create upload file", zap.Error(err))
		h.markUploadFailed(c.Request.Context(), uploadID.String(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	// Copy with hash calculation
	hasher := sha256.New()
	writer := io.MultiWriter(outFile, hasher)

	written, copyErr := io.Copy(writer, file)
	var syncErr error
	if copyErr == nil {
		syncErr = outFile.Sync()
	}
	closeErr := outFile.Close()
	err = errors.Join(copyErr, syncErr, closeErr)
	if err == nil && written != header.Size {
		err = fmt.Errorf("received %d bytes, expected %d", written, header.Size)
	}

	if err != nil {
		h.safeLogger().Error("failed to save file", zap.Error(err))
		if removeErr := os.Remove(uploadPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			h.safeLogger().Warn("failed to remove incomplete upload", zap.String("path", uploadPath), zap.Error(removeErr))
		}
		h.markUploadFailed(c.Request.Context(), uploadID.String(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "upload failed"})
		return
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))

	h.safeLogger().Info("file uploaded",
		zap.String("upload_id", uploadID.String()),
		zap.String("filename", originalName),
		zap.Int64("size", written),
		zap.String("checksum", checksum))

	// Update upload status
	if err := h.markUploadProcessing(c.Request.Context(), uploadID.String(), written, checksum); err != nil {
		h.safeLogger().Error("failed to mark upload as processing", zap.String("upload_id", uploadID.String()), zap.Error(err))
		if removeErr := os.Remove(uploadPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			h.safeLogger().Warn("failed to remove untracked upload", zap.String("path", uploadPath), zap.Error(removeErr))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start image processing"})
		return
	}

	// Convert to QCOW2 (async in background)
	go h.processUpload(uploadID.String(), templateID.String(), name, templateSlug, description, originalName, uploadPath, templatesDir, checksum, minVCPU, minMemoryMB, osType)

	c.JSON(http.StatusAccepted, gin.H{
		"upload_id":   uploadID.String(),
		"template_id": templateID.String(),
		"message":     "upload received, processing in background",
		"status":      "processing",
	})
}

func parsePositiveTemplateResource(value, field string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 || int64(parsed) > int64(^uint32(0)>>1) {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	return parsed, nil
}

func (h *VMTemplateHandler) markUploadProcessing(ctx context.Context, uploadID string, written int64, checksum string) error {
	result, err := h.db.Pool.Exec(ctx, `
		UPDATE uploads
		SET status = 'processing', uploaded_size = $2, checksum_actual = $3,
		    error_message = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'uploading'
	`, uploadID, written, checksum)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("upload processing update affected %d rows", result.RowsAffected())
	}
	return nil
}

func copyFileDurably(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}

	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Join(fmt.Errorf("create destination: %w", err), source.Close())
	}

	_, copyErr := io.Copy(destination, source)
	var syncErr error
	if copyErr == nil {
		syncErr = destination.Sync()
	}
	destinationCloseErr := destination.Close()
	sourceCloseErr := source.Close()

	if err := errors.Join(copyErr, syncErr, destinationCloseErr, sourceCloseErr); err != nil {
		if removeErr := os.Remove(destinationPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("remove incomplete destination: %w", removeErr))
		}
		return err
	}

	return nil
}

func (h *VMTemplateHandler) markUploadFailed(ctx context.Context, uploadID string, processingErr error) {
	message := processingErr.Error()
	messageRunes := []rune(message)
	if len(messageRunes) > 4000 {
		message = string(messageRunes[:4000])
	}
	result, err := h.db.Pool.Exec(ctx, `
		UPDATE uploads
		SET status = 'failed', error_message = $2, updated_at = NOW()
		WHERE id = $1 AND status <> 'completed'
	`, uploadID, message)
	if err != nil {
		h.safeLogger().Error("failed to mark upload as failed",
			zap.String("upload_id", uploadID),
			zap.Error(err))
		return
	}
	if result.RowsAffected() != 1 {
		h.safeLogger().Warn("upload failure status affected an unexpected number of rows",
			zap.String("upload_id", uploadID),
			zap.Int64("rows_affected", result.RowsAffected()))
	}
}

func (h *VMTemplateHandler) removeProcessingFile(path, operation string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.safeLogger().Warn("failed to clean VM template processing file",
			zap.String("operation", operation),
			zap.String("path", path),
			zap.Error(err))
	}
}

type cappedCommandOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (output *cappedCommandOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := output.limit - output.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = output.buffer.Write(data)
	}
	return written, nil
}

func runCommandWithCappedOutput(command *exec.Cmd) ([]byte, error) {
	output := &cappedCommandOutput{limit: maxVMConversionOutputBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.buffer.Bytes(), err
}

func inspectQCOW2Image(ctx context.Context, imagePath string) error {
	command := exec.CommandContext(ctx, "qemu-img", "info", "-f", "qcow2", "--output=json", imagePath)
	output, err := runCommandWithCappedOutput(command)
	if err != nil {
		return fmt.Errorf("inspect qcow2 image: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (h *VMTemplateHandler) processUpload(uploadID, templateID, name, templateSlug, description, originalName, uploadPath, templatesDir, checksum string, minVCPU, minMemoryMB int, osType string) {
	ctx := context.Background()
	conversionCtx, cancelConversion := context.WithTimeout(ctx, vmTemplateConversionTimeout)
	defer cancelConversion()

	// The template ID keeps untrusted names out of the filesystem and prevents a
	// duplicate template name from overwriting an existing image.
	qcow2Path := filepath.Join(templatesDir, templateID+".qcow2")
	if _, err := os.Lstat(qcow2Path); err == nil {
		pathErr := errors.New("processed image path already exists")
		h.safeLogger().Error("refusing to overwrite VM template image", zap.String("path", qcow2Path), zap.Error(pathErr))
		h.markUploadFailed(ctx, uploadID, pathErr)
		h.removeProcessingFile(uploadPath, "reject existing destination")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		pathErr := fmt.Errorf("inspect processed image path: %w", err)
		h.safeLogger().Error("failed to inspect VM template image path", zap.String("path", qcow2Path), zap.Error(pathErr))
		h.markUploadFailed(ctx, uploadID, pathErr)
		h.removeProcessingFile(uploadPath, "reject inaccessible destination")
		return
	}

	ext := strings.ToLower(filepath.Ext(uploadPath))
	var diskSizeGB float64

	if ext == ".qcow2" {
		// Already QCOW2, just move it
		if err := os.Rename(uploadPath, qcow2Path); err != nil {
			// Copy if rename fails (cross-device)
			if copyErr := copyFileDurably(uploadPath, qcow2Path); copyErr != nil {
				h.safeLogger().Error("failed to copy qcow2 file", zap.Error(copyErr))
				h.markUploadFailed(ctx, uploadID, copyErr)
				h.removeProcessingFile(uploadPath, "remove failed qcow2 upload")
				return
			}
			if removeErr := os.Remove(uploadPath); removeErr != nil {
				h.safeLogger().Warn("failed to remove copied upload",
					zap.String("upload_id", uploadID),
					zap.String("path", uploadPath),
					zap.Error(removeErr))
			}
		}
	} else if ext == ".ova" || ext == ".vmdk" {
		// Convert using qemu-img
		var inputPath string

		if ext == ".ova" {
			// Extract only the VMDK payload. The archive helper rejects traversal
			// paths and links and never writes an archive-supplied filename.
			extractDir := filepath.Join(filepath.Dir(uploadPath), uploadID+"-extracted")
			if err := os.Mkdir(extractDir, 0700); err != nil {
				extractErr := fmt.Errorf("create OVA extraction directory: %w", err)
				h.safeLogger().Error("failed to prepare OVA extraction", zap.Error(extractErr))
				h.markUploadFailed(ctx, uploadID, extractErr)
				h.removeProcessingFile(uploadPath, "remove unprocessable OVA")
				return
			}
			defer func() {
				if err := os.RemoveAll(extractDir); err != nil {
					h.safeLogger().Warn("failed to clean OVA extraction directory", zap.String("path", extractDir), zap.Error(err))
				}
			}()

			extractedPath, extractErr := vmimage.ExtractVMDK(conversionCtx, uploadPath, extractDir)
			if extractErr != nil {
				h.safeLogger().Error("failed to extract OVA", zap.Error(extractErr))
				h.markUploadFailed(ctx, uploadID, extractErr)
				if removeErr := os.Remove(uploadPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					h.safeLogger().Warn("failed to remove rejected OVA", zap.String("path", uploadPath), zap.Error(removeErr))
				}
				return
			}
			inputPath = extractedPath
		} else {
			inputPath = uploadPath
		}

		// Convert to QCOW2
		h.safeLogger().Info("converting to qcow2", zap.String("input", inputPath), zap.String("output", qcow2Path))
		cmd := exec.CommandContext(conversionCtx, "qemu-img", "convert", "-f", "vmdk", "-O", "qcow2", inputPath, qcow2Path)
		output, err := runCommandWithCappedOutput(cmd)
		if err != nil {
			convertErr := fmt.Errorf("qemu-img conversion failed: %w", err)
			if errors.Is(conversionCtx.Err(), context.DeadlineExceeded) {
				convertErr = fmt.Errorf("qemu-img conversion timed out after %s", vmTemplateConversionTimeout)
			}
			h.safeLogger().Error("qemu-img convert failed", zap.Error(convertErr), zap.ByteString("output", output))
			h.markUploadFailed(ctx, uploadID, convertErr)
			for _, path := range []string{uploadPath, qcow2Path} {
				if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					h.safeLogger().Warn("failed to remove failed conversion file", zap.String("path", path), zap.Error(removeErr))
				}
			}
			return
		}

		// Clean up original
		if removeErr := os.Remove(uploadPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			h.safeLogger().Warn("failed to remove converted upload", zap.String("path", uploadPath), zap.Error(removeErr))
		}
	}

	// Get disk size
	info, err := os.Stat(qcow2Path)
	if err != nil {
		statErr := fmt.Errorf("stat processed qcow2: %w", err)
		h.safeLogger().Error("failed to inspect processed qcow2",
			zap.String("upload_id", uploadID),
			zap.String("path", qcow2Path),
			zap.Error(err))
		h.markUploadFailed(ctx, uploadID, statErr)
		h.removeProcessingFile(qcow2Path, "remove uninspectable image")
		return
	}
	if !info.Mode().IsRegular() || info.Size() < 1 {
		imageErr := errors.New("processed qcow2 is not a non-empty regular file")
		h.safeLogger().Error("invalid processed qcow2", zap.String("upload_id", uploadID), zap.Error(imageErr))
		h.markUploadFailed(ctx, uploadID, imageErr)
		h.removeProcessingFile(qcow2Path, "remove invalid image")
		return
	}
	if err := inspectQCOW2Image(conversionCtx, qcow2Path); err != nil {
		h.safeLogger().Error("processed qcow2 failed validation", zap.String("upload_id", uploadID), zap.Error(err))
		h.markUploadFailed(ctx, uploadID, errors.New("processed image is not a valid qcow2 file"))
		h.removeProcessingFile(qcow2Path, "remove invalid qcow2 image")
		return
	}
	imageSizeBytes := info.Size()
	diskSizeGB = float64(imageSizeBytes) / (1024 * 1024 * 1024)

	// Determine original format
	originalFormat := "qcow2"
	if strings.HasSuffix(strings.ToLower(originalName), ".ova") {
		originalFormat = "ova"
	} else if strings.HasSuffix(strings.ToLower(originalName), ".vmdk") {
		originalFormat = "vmdk"
	}

	// Create the template and complete the upload atomically. If the status
	// update fails, the active template must not remain visible.
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.safeLogger().Error("failed to begin template transaction", zap.Error(err))
		h.markUploadFailed(ctx, uploadID, err)
		h.removeProcessingFile(qcow2Path, "rollback transaction start")
		return
	}

	insertResult, err := tx.Exec(ctx, `
		INSERT INTO vm_templates (
			id, upload_id, name, slug, description, image_path, original_format, original_path,
			image_size, vcpu, memory_mb, disk_gb, os_type, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::vm_image_format, $8, $9, $10, $11, $12, $13, true, NOW(), NOW())
	`, templateID, uploadID, name, templateSlug, description, qcow2Path, originalFormat, nil,
		imageSizeBytes, minVCPU, minMemoryMB, int((imageSizeBytes+(1024*1024*1024)-1)/(1024*1024*1024)), osType)

	if err != nil {
		_ = tx.Rollback(ctx)
		h.safeLogger().Error("failed to create template record", zap.Error(err))
		h.markUploadFailed(ctx, uploadID, err)
		h.removeProcessingFile(qcow2Path, "rollback template insert")
		return
	}
	if insertResult.RowsAffected() != 1 {
		_ = tx.Rollback(ctx)
		err := fmt.Errorf("template insert affected %d rows", insertResult.RowsAffected())
		h.safeLogger().Error("failed to create template record", zap.Error(err))
		h.markUploadFailed(ctx, uploadID, err)
		h.removeProcessingFile(qcow2Path, "rollback unexpected template insert")
		return
	}

	result, err := tx.Exec(ctx, `
		UPDATE uploads
		SET status = 'completed', processed_path = $2, checksum_actual = $3,
		    error_message = NULL, completed_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = 'processing'
	`, uploadID, qcow2Path, checksum)
	if err != nil {
		_ = tx.Rollback(ctx)
		h.safeLogger().Error("failed to mark upload as completed", zap.String("upload_id", uploadID), zap.Error(err))
		h.markUploadFailed(ctx, uploadID, err)
		h.removeProcessingFile(qcow2Path, "rollback upload completion")
		return
	}
	if result.RowsAffected() != 1 {
		_ = tx.Rollback(ctx)
		err := fmt.Errorf("upload completion updated %d rows", result.RowsAffected())
		h.safeLogger().Error("failed to mark upload as completed", zap.String("upload_id", uploadID), zap.Error(err))
		h.markUploadFailed(ctx, uploadID, err)
		h.removeProcessingFile(qcow2Path, "rollback unexpected upload state")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.safeLogger().Error("failed to commit completed template", zap.String("upload_id", uploadID), zap.Error(err))
		var committed bool
		verifyErr := h.db.Pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM vm_templates t
				JOIN uploads u ON u.id = t.upload_id
				WHERE t.id = $1 AND u.id = $2 AND u.status = 'completed'
			)
		`, templateID, uploadID).Scan(&committed)
		if verifyErr == nil && !committed {
			h.markUploadFailed(ctx, uploadID, err)
			h.removeProcessingFile(qcow2Path, "reconcile failed commit")
		} else if verifyErr != nil {
			h.safeLogger().Error("failed to reconcile template commit", zap.String("upload_id", uploadID), zap.Error(verifyErr))
		}
		return
	}

	h.safeLogger().Info("template created",
		zap.String("template_id", templateID),
		zap.String("name", name),
		zap.Float64("disk_gb", diskSizeGB))
}

// GetUploadStatus returns the status of an upload
// GET /api/v1/admin/vm-templates/upload/:id/status
func (h *VMTemplateHandler) GetUploadStatus(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	uploadID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload ID"})
		return
	}

	var status, filename string
	var sizeBytes int64
	var errorMessage *string
	err = h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT status, filename, total_size, error_message FROM uploads WHERE id = $1
	`, uploadID).Scan(&status, &filename, &sizeBytes, &errorMessage)

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}
	if err != nil {
		h.safeLogger().Error("failed to fetch upload status", zap.String("upload_id", uploadID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch upload status"})
		return
	}

	// Check if template was created
	var templateID string
	templateErr := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT id FROM vm_templates WHERE upload_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, uploadID).Scan(&templateID)

	response := gin.H{
		"upload_id": uploadID,
		"status":    status,
		"filename":  filename,
		"size":      sizeBytes,
	}

	if templateErr == nil {
		response["template_id"] = templateID
	} else if !errors.Is(templateErr, pgx.ErrNoRows) {
		h.safeLogger().Error("failed to resolve processed template", zap.String("upload_id", uploadID.String()), zap.Error(templateErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch upload status"})
		return
	}
	if errorMessage != nil && *errorMessage != "" {
		response["error"] = *errorMessage
	}

	c.JSON(http.StatusOK, response)
}

func removeFileWithin(rootPath, targetPath string) (returnErr error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("resolve managed root: %w", err)
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolve managed file: %w", err)
	}
	if !pathWithin(absRoot, absTarget) {
		return fmt.Errorf("refusing to remove file outside managed directory")
	}

	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return fmt.Errorf("open managed root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()

	relativePath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return fmt.Errorf("resolve managed relative path: %w", err)
	}

	info, err := root.Lstat(relativePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect managed file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed image path is not a regular file")
	}
	if err := root.Remove(relativePath); err != nil {
		return fmt.Errorf("remove managed file: %w", err)
	}
	return nil
}

func pathWithin(rootPath, targetPath string) bool {
	relativePath, err := filepath.Rel(rootPath, targetPath)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator))
}

// Delete removes a VM template
// DELETE /api/v1/admin/vm-templates/:id
func (h *VMTemplateHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.safeLogger().Error("failed to begin template deletion", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	// Lock the template row so a challenge resource cannot be attached while
	// deletion checks are in flight.
	var imagePath string
	err = tx.QueryRow(c.Request.Context(),
		`SELECT image_path FROM vm_templates WHERE id = $1 FOR UPDATE`, templateID).Scan(&imagePath)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		h.safeLogger().Error("failed to lock template for deletion", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	// Check if any challenges use this template
	var challengeCount int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM challenge_resources 
		WHERE resource_type = 'vm' AND vm_template_id = $1
	`, templateID).Scan(&challengeCount); err != nil {
		h.safeLogger().Error("failed to inspect template references", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	if challengeCount > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":      "template is used by challenges",
			"challenges": challengeCount,
		})
		return
	}

	// Delete template record
	result, err := tx.Exec(c.Request.Context(),
		`DELETE FROM vm_templates WHERE id = $1`, templateID)
	if err != nil {
		if postgresErrorCode(err) == "23503" {
			c.JSON(http.StatusConflict, gin.H{"error": "template is still referenced by infrastructure records"})
			return
		}
		h.safeLogger().Error("failed to delete template", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}
	if result.RowsAffected() != 1 {
		h.safeLogger().Error("template deletion affected an unexpected number of rows", zap.String("template_id", templateID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.safeLogger().Error("failed to commit template deletion", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	// Uploaded template images are managed under the templates directory.
	// Registered external paths are references only and must never be deleted.
	cleanupPending := false
	managedRoot, rootErr := filepath.Abs(filepath.Join(vmImageBasePath, "templates"))
	managedImage, imageErr := filepath.Abs(imagePath)
	if rootErr == nil && imageErr == nil && imagePath != "" && pathWithin(managedRoot, managedImage) {
		if err := removeFileWithin(filepath.Join(vmImageBasePath, "templates"), imagePath); err != nil {
			cleanupPending = true
			h.safeLogger().Warn("template image not removed",
				zap.String("template_id", templateID.String()),
				zap.String("image_path", imagePath),
				zap.Error(err))
		}
	}

	response := gin.H{"message": "template deleted"}
	if cleanupPending {
		response["warning"] = "template record was deleted, but its managed image requires manual cleanup"
	}
	c.JSON(http.StatusOK, response)
}

// Update modifies a VM template
// PUT /api/v1/admin/vm-templates/:id
func (h *VMTemplateHandler) Update(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

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
	if req.Name == nil && req.Description == nil && req.VCPU == nil && req.MemoryMB == nil && req.IsActive == nil && req.IsPublic == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || len(trimmed) > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be between 1 and 200 characters"})
			return
		}
		req.Name = &trimmed
	}
	if (req.VCPU != nil && (*req.VCPU < 1 || int64(*req.VCPU) > int64(^uint32(0)>>1))) ||
		(req.MemoryMB != nil && (*req.MemoryMB < 1 || int64(*req.MemoryMB) > int64(^uint32(0)>>1))) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vcpu and memory_mb must be positive integers"})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(), `
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
		h.safeLogger().Error("failed to update template", zap.String("template_id", templateID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update template"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if result.RowsAffected() != 1 {
		h.safeLogger().Error("template update affected an unexpected number of rows", zap.String("template_id", templateID.String()), zap.Int64("rows_affected", result.RowsAffected()))
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
	if !h.ready(c) {
		return
	}
	var req TemplateRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ImagePath = strings.TrimSpace(req.ImagePath)
	req.OSType = strings.TrimSpace(req.OSType)
	req.OSVariant = strings.TrimSpace(req.OSVariant)
	req.OSName = strings.TrimSpace(req.OSName)
	req.NetworkMode = strings.TrimSpace(req.NetworkMode)
	if req.Name == "" || len(req.Name) > 200 || len(req.ImagePath) > 500 || len(req.OSType) > 50 ||
		len(req.OSVariant) > 100 || len(req.OSName) > 200 || len(req.NetworkMode) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "one or more template fields are empty or exceed their maximum length"})
		return
	}
	if !filepath.IsAbs(req.ImagePath) || strings.ToLower(filepath.Ext(req.ImagePath)) != ".qcow2" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image_path must be an absolute .qcow2 path"})
		return
	}
	resolvedPath, err := filepath.EvalSymlinks(filepath.Clean(req.ImagePath))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file not found"})
		return
	}
	if len(resolvedPath) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resolved image_path exceeds 500 characters"})
		return
	}
	req.ImagePath = resolvedPath

	// Validate file exists
	info, err := os.Stat(req.ImagePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file not found"})
		return
	}
	if !info.Mode().IsRegular() || info.Size() < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image_path must reference a non-empty regular file"})
		return
	}
	inspectionCtx, cancelInspection := context.WithTimeout(c.Request.Context(), vmTemplateInspectionTimeout)
	defer cancelInspection()
	if err := inspectQCOW2Image(inspectionCtx, req.ImagePath); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			h.safeLogger().Error("qemu-img is unavailable while registering template", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VM image inspection service unavailable"})
			return
		}
		h.safeLogger().Warn("rejected invalid registered qcow2 image", zap.String("image_path", req.ImagePath), zap.Error(err))
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "image_path is not a valid qcow2 image"})
		return
	}

	// Set defaults
	if req.VCPU == 0 {
		req.VCPU = 1
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 1024
	}
	if req.OSType == "" {
		req.OSType = "linux"
	}
	if req.NetworkMode == "" {
		req.NetworkMode = "nat"
	}
	if req.DiskGB < 1 || req.VCPU < 1 || req.MemoryMB < 1 ||
		int64(req.DiskGB) > int64(^uint32(0)>>1) || int64(req.VCPU) > int64(^uint32(0)>>1) || int64(req.MemoryMB) > int64(^uint32(0)>>1) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "disk_gb, vcpu, and memory_mb must be positive integers"})
		return
	}
	switch req.NetworkMode {
	case "nat", "bridge", "isolated":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "network_mode must be nat, bridge, or isolated"})
		return
	}

	// Generate slug from name
	templateSlug := slug.Make(req.Name)
	if templateSlug == "" || len(templateSlug) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain URL-safe letters or numbers"})
		return
	}

	templateID := uuid.New()

	result, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO vm_templates (
			id, name, slug, description, image_path, original_format, image_size,
			disk_gb, vcpu, memory_mb, os_type, os_variant, os_name, network_mode,
			is_active, is_public, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'qcow2', $6, $7, $8, $9, $10, $11, $12, $13, true, false, NOW(), NOW())
	`, templateID, req.Name, templateSlug, req.Description, req.ImagePath, info.Size(),
		req.DiskGB, req.VCPU, req.MemoryMB, req.OSType, req.OSVariant, req.OSName, req.NetworkMode)

	if err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "a template with this name-derived slug already exists"})
			return
		}
		h.safeLogger().Error("failed to register template", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register template"})
		return
	}
	if result.RowsAffected() != 1 {
		h.safeLogger().Error("template registration affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register template"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":      templateID.String(),
		"message": "template registered",
	})
}

type InfrastructureInstanceResponse struct {
	ID            string  `json:"id"`
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	ChallengeID   string  `json:"challenge_id"`
	ChallengeName string  `json:"challenge_name"`
	VMName        string  `json:"vm_name"`
	Status        string  `json:"status"`
	IPAddress     *string `json:"ip_address"`
	CreatedAt     int64   `json:"created_at"`
	ExpiresAt     *int64  `json:"expires_at,omitempty"`
}

func (h *VMTemplateHandler) listActiveInstances(c *gin.Context, vmOnly bool) {
	if !h.ready(c) {
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT i.id,
		       COALESCE(i.user_id, i.session_id)::text,
		       COALESCE(u.username, tt.team_name, 'unknown'),
		       i.challenge_id, ch.name,
		       COALESCE(i.vm_id, i.container_name, i.container_id, ''),
		       i.status, i.ip_address, i.created_at, i.expires_at
		FROM instances i
		LEFT JOIN users u ON i.user_id = u.id
		LEFT JOIN sessions s ON i.session_id = s.id
		LEFT JOIN team_tokens tt ON s.token_id = tt.id
		JOIN challenges ch ON i.challenge_id = ch.id
		WHERE i.status = 'running'
		  AND (($1::boolean AND ch.resource_type = 'vm') OR (NOT $1::boolean AND ch.resource_type != 'vm'))
		ORDER BY i.created_at DESC
	`, vmOnly)
	if err != nil {
		h.safeLogger().Error("failed to list active infrastructure instances", zap.Bool("vm_only", vmOnly), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}
	defer rows.Close()

	instances := make([]InfrastructureInstanceResponse, 0)
	for rows.Next() {
		var inst InfrastructureInstanceResponse
		var createdAt time.Time
		var expiresAt *time.Time

		if err := rows.Scan(
			&inst.ID, &inst.UserID, &inst.Username, &inst.ChallengeID, &inst.ChallengeName,
			&inst.VMName, &inst.Status, &inst.IPAddress, &createdAt, &expiresAt,
		); err != nil {
			h.safeLogger().Error("failed to scan active infrastructure instance", zap.Bool("vm_only", vmOnly), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
			return
		}

		inst.CreatedAt = createdAt.Unix()
		if expiresAt != nil {
			unix := expiresAt.Unix()
			inst.ExpiresAt = &unix
		}
		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		h.safeLogger().Error("failed while listing active infrastructure instances", zap.Bool("vm_only", vmOnly), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"total":     len(instances),
	})
}

// ListActiveInstances returns all running VM instances.
// GET /api/v1/admin/infrastructure/instances
func (h *VMTemplateHandler) ListActiveInstances(c *gin.Context) {
	h.listActiveInstances(c, true)
}

// ListActiveDockerInstances returns all running Docker instances.
// GET /api/v1/admin/infrastructure/docker-instances
func (h *VMTemplateHandler) ListActiveDockerInstances(c *gin.Context) {
	h.listActiveInstances(c, false)
}
