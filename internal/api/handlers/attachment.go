package handlers

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AttachmentResponse is the public representation of a challenge attachment
type AttachmentResponse struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   int64  `json:"created_at"`
}

// maxAttachmentSize is the upper limit for a single-request attachment upload (500 MB)
const maxAttachmentSize = 500 * 1024 * 1024

// allowedAttachmentTypes whitelists MIME types and extensions for challenge files.
// Path-traversal safety: we never use the original filename as a storage path —
// we always use a UUID-based key.
var allowedAttachmentExtensions = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
	".7z": true, ".rar": true, ".xz": true,
	".pdf": true, ".txt": true, ".md": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
	".py": true, ".c": true, ".cpp": true, ".h": true, ".go": true, ".js": true,
	".ts": true, ".sh": true, ".rb": true, ".java": true,
	".pcap": true, ".pcapng": true, ".cap": true,
	".bin": true, ".exe": true, ".elf": true, ".out": true,
	".iso": true, ".img": true,
	".json": true, ".xml": true, ".yaml": true, ".yml": true, ".toml": true,
	".sql": true,
	"":     true, // no extension (binaries named without extension)
}

// sanitiseFilename strips directory components and control characters so that
// the returned name is safe to embed in a Content-Disposition header.
func sanitiseFilename(name string) string {
	// Strip directory components
	name = filepath.Base(name)
	// Remove any characters that are not printable ASCII
	var b strings.Builder
	for _, r := range name {
		if r > 0x1F && r < 0x7F && unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if result == "" || result == "." {
		return "file"
	}
	return result
}

// UploadAttachment allows an admin to attach a file to a challenge.
// POST /api/v1/admin/challenges/:id/attachments
func (h *AttachmentHandler) Upload(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}

	// Verify challenge exists
	var exists bool
	if err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM challenges WHERE id = $1)`, challengeID,
	).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAttachmentSize)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large or invalid multipart form"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing 'file' field"})
		return
	}
	defer file.Close()

	if header.Size > maxAttachmentSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds 500 MB limit"})
		return
	}

	originalName := sanitiseFilename(header.Filename)
	ext := strings.ToLower(filepath.Ext(originalName))
	if !allowedAttachmentExtensions[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file type '%s' is not allowed", ext)})
		return
	}

	// Detect content type from the extension / header
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		if ext != "" {
			contentType = mime.TypeByExtension(ext)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	// Generate a UUID storage key to prevent path traversal
	attachmentID := uuid.New()
	storageKey := fmt.Sprintf("challenge-attachments/%s/%s", challengeID, attachmentID.String())

	if err := h.storageSvc.Upload(c.Request.Context(), storageKey, file, header.Size); err != nil {
		h.logger.Error("failed to store attachment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store file"})
		return
	}

	// Optional fields
	description := c.PostForm("description")
	sortOrder := 0
	if soStr := c.PostForm("sort_order"); soStr != "" {
		if n, err := fmt.Sscanf(soStr, "%d", &sortOrder); n != 1 || err != nil {
			sortOrder = 0
		}
	}

	// Get uploader ID
	uploaderID, _ := c.Get("user_id")
	uploaderUID := uploaderID.(uuid.UUID)

	// Save metadata
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO challenge_attachments
		 (id, challenge_id, uploaded_by, filename, file_size, content_type, storage_key, description, sort_order, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
		attachmentID, challengeID, uploaderUID,
		originalName, header.Size, contentType,
		storageKey, description, sortOrder,
	)
	if err != nil {
		// Best-effort cleanup of stored file
		h.storageSvc.Delete(c.Request.Context(), storageKey) //nolint:errcheck
		h.logger.Error("failed to save attachment metadata", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save attachment"})
		return
	}

	h.logger.Info("attachment uploaded",
		zap.String("challenge_id", challengeID),
		zap.String("attachment_id", attachmentID.String()),
		zap.String("filename", originalName),
	)

	c.JSON(http.StatusCreated, gin.H{
		"id":           attachmentID.String(),
		"filename":     originalName,
		"file_size":    header.Size,
		"content_type": contentType,
		"description":  description,
		"sort_order":   sortOrder,
	})
}

// ListAttachments returns all attachments for a challenge (admin endpoint).
// GET /api/v1/admin/challenges/:id/attachments
func (h *AttachmentHandler) List(c *gin.Context) {
	challengeID := c.Param("id")
	attachments, err := h.queryAttachments(c, challengeID)
	if err != nil {
		h.logger.Error("failed to list attachments", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch attachments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"attachments": attachments})
}

// DeleteAttachment removes an attachment (admin).
// DELETE /api/v1/admin/challenges/:id/attachments/:attachment_id
func (h *AttachmentHandler) Delete(c *gin.Context) {
	challengeID := c.Param("id")
	attachmentID := c.Param("attachment_id")

	var storageKey string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT storage_key FROM challenge_attachments WHERE id = $1 AND challenge_id = $2`,
		attachmentID, challengeID,
	).Scan(&storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	// Remove stored file (best-effort)
	if err := h.storageSvc.Delete(c.Request.Context(), storageKey); err != nil {
		h.logger.Warn("failed to delete attachment file", zap.Error(err), zap.String("key", storageKey))
	}

	// Remove metadata
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM challenge_attachments WHERE id = $1`, attachmentID,
	); err != nil {
		h.logger.Error("failed to delete attachment metadata", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete attachment"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "attachment deleted"})
}

// Download serves an attachment file to participants.
// GET /api/v1/challenges/:slug/attachments/:attachment_id/download
func (h *AttachmentHandler) Download(c *gin.Context) {
	slug := c.Param("slug")
	attachmentID := c.Param("attachment_id")

	// Look up attachment (join with challenge to validate slug ownership and published status)
	var storageKey, filename, contentType string
	var fileSize int64
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT ca.storage_key, ca.filename, COALESCE(ca.content_type, 'application/octet-stream'), ca.file_size
		 FROM challenge_attachments ca
		 JOIN challenges c ON c.id = ca.challenge_id
		 WHERE ca.id = $1 AND c.slug = $2 AND c.status = 'published'`,
		attachmentID, slug,
	).Scan(&storageKey, &filename, &contentType, &fileSize)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	// Open the file from storage
	reader, err := h.storageSvc.Download(c.Request.Context(), storageKey)
	if err != nil {
		h.logger.Error("failed to open attachment", zap.Error(err), zap.String("key", storageKey))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}
	defer reader.Close()

	// Force download with original filename; never render inline to prevent XSS
	safeFilename := sanitiseFilename(filename)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeFilename))
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", fmt.Sprintf("%d", fileSize))
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")

	c.Status(http.StatusOK)
	if _, copyErr := io.Copy(c.Writer, reader); copyErr != nil {
		h.logger.Warn("attachment download interrupted", zap.Error(copyErr), zap.String("key", storageKey))
	}
}

// ListPublic is used by the challenge detail endpoint to embed attachments.
func (h *AttachmentHandler) ListPublic(c *gin.Context, challengeID string) ([]AttachmentResponse, error) {
	return h.queryAttachments(c, challengeID)
}

func (h *AttachmentHandler) queryAttachments(c *gin.Context, challengeID string) ([]AttachmentResponse, error) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, filename, file_size, COALESCE(content_type, ''), COALESCE(description, ''), sort_order, created_at
		 FROM challenge_attachments
		 WHERE challenge_id = $1
		 ORDER BY sort_order, created_at`,
		challengeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []AttachmentResponse
	for rows.Next() {
		var a AttachmentResponse
		var createdAt time.Time
		if err := rows.Scan(&a.ID, &a.Filename, &a.FileSize, &a.ContentType, &a.Description, &a.SortOrder, &createdAt); err != nil {
			continue
		}
		a.CreatedAt = createdAt.Unix()
		attachments = append(attachments, a)
	}
	if attachments == nil {
		attachments = []AttachmentResponse{}
	}
	return attachments, nil
}
