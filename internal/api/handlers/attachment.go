package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type AttachmentResponse struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   int64  `json:"created_at"`
	URL         string `json:"url,omitempty"`    // set => external handout (download redirects here)
	Sha256      string `json:"sha256,omitempty"` // optional integrity check for large external handouts
}

// upper limit for a single-request attachment upload (500 mb)
const maxAttachmentSize = 500 * 1024 * 1024

const (
	maxAttachmentRequestOverhead = 1 * 1024 * 1024
	maxAttachmentFilenameLength  = 500
	maxAttachmentDescription     = 5000
)

// whitelists mime types and extensions for challenge files.
// path-traversal safety: we never use the original filename as a storage path —
// we always use a uuid-based key.
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
	// challenge source handouts: blockchain (rust/move/solidity/vyper),
	// systems (asm/c++), web, and other language sources are text and download-only.
	".rs": true, ".move": true, ".sol": true, ".vy": true, ".lock": true,
	".asm": true, ".s": true, ".hpp": true, ".cc": true, ".cxx": true, ".hxx": true,
	".html": true, ".htm": true, ".css": true, ".php": true,
	".lua": true, ".pl": true, ".kt": true, ".swift": true, ".cs": true, ".sage": true,
	".cfg": true, ".conf": true, ".ini": true, ".csv": true, ".env": true,
	"":     true, // no extension (binaries named without extension)
	// NB: big/exotic forensics artifacts (memory + disk images, sqlite DBs) are
	// handed out ZIPPED (.zip, above) — compresses + saves space — or hosted on the
	// GCS bucket as an external-url handout, so the allowlist stays lean.
}

// strips directory components and control characters so the returned name
// is safe to embed in a content-disposition header.
func sanitiseFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if r > 0x1F && r < 0x7F && r != '"' && r != '\\' && unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	result := strings.TrimSpace(b.String())
	if result == "" || result == "." {
		return "file"
	}
	return result
}

// POST /api/v1/admin/challenges/:id/attachments
func (h *AttachmentHandler) Upload(c *gin.Context) {
	uploaderUID, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	if h.storageSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "attachment storage is unavailable"})
		return
	}

	var exists bool
	if err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM challenges WHERE id = $1)`, challengeID,
	).Scan(&exists); err != nil {
		h.logger.Error("failed to query challenge for attachment upload", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload attachment"})
		return
	} else if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// allow a small amount of multipart framing overhead in addition to the file
	// limit, then enforce the file's own size below.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAttachmentSize+maxAttachmentRequestOverhead)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds 500 MB limit"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart form"})
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing 'file' field"})
		return
	}
	defer file.Close()

	if header.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file must not be empty"})
		return
	}
	if header.Size > maxAttachmentSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds 500 MB limit"})
		return
	}

	originalName := sanitiseFilename(header.Filename)
	if len(originalName) > maxAttachmentFilenameLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename is too long"})
		return
	}
	ext := strings.ToLower(filepath.Ext(originalName))
	if !allowedAttachmentExtensions[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file type '%s' is not allowed", ext)})
		return
	}

	// derive the response type from the accepted extension rather than trusting
	// a client-supplied multipart header.
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	description := strings.TrimSpace(c.PostForm("description"))
	if len(description) > maxAttachmentDescription {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description is too long"})
		return
	}
	sortOrder := 0
	if soStr := c.PostForm("sort_order"); soStr != "" {
		parsed, err := strconv.ParseInt(soStr, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sort_order must be an integer"})
			return
		}
		sortOrder = int(parsed)
	}

	// generate a uuid storage key to prevent path traversal
	attachmentID := uuid.New()
	storageKey := fmt.Sprintf("challenge-attachments/%s/%s", challengeID, attachmentID.String())

	if err := h.storageSvc.Upload(c.Request.Context(), storageKey, file, header.Size); err != nil {
		h.logger.Error("failed to store attachment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store file"})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO challenge_attachments
		 (id, challenge_id, uploaded_by, filename, file_size, content_type, storage_key, description, sort_order, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
		attachmentID, challengeID, uploaderUID,
		originalName, header.Size, contentType,
		storageKey, description, sortOrder,
	)
	if err != nil {
		if cleanupErr := h.storageSvc.Delete(c.Request.Context(), storageKey); cleanupErr != nil {
			h.logger.Warn("failed to clean up attachment after metadata error", zap.Error(cleanupErr), zap.String("key", storageKey))
		}
		h.logger.Error("failed to save attachment metadata", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save attachment"})
		return
	}
	if result.RowsAffected() != 1 {
		if cleanupErr := h.storageSvc.Delete(c.Request.Context(), storageKey); cleanupErr != nil {
			h.logger.Warn("failed to clean up attachment after metadata error", zap.Error(cleanupErr), zap.String("key", storageKey))
		}
		h.logger.Error("attachment metadata insert affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()))
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

// CreateLinkRequest registers an EXTERNAL handout — a file hosted off-platform
// (e.g. the public GCS bucket) that's too big for Anvil's storage backend.
type CreateLinkRequest struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Sha256      string `json:"sha256"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
}

// CreateLink adds an external-URL handout to a challenge. The public download
// endpoint 302-redirects players to the URL; nothing is stored in the backend.
func (h *AttachmentHandler) CreateLink(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	var req CreateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and url are required"})
		return
	}
	if u, err := url.Parse(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be an absolute http(s) URL"})
		return
	}

	uploaderUID, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// best-effort HEAD to record size + type for the UI; never blocks the create.
	var fileSize int64
	contentType := ""
	if ct, size, ok := headMeta(c.Request.Context(), req.URL); ok {
		contentType, fileSize = ct, size
	}
	if contentType == "" {
		if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(req.Name))); t != "" {
			contentType = t
		} else {
			contentType = "application/octet-stream"
		}
	}
	if fileSize < 0 {
		fileSize = 0
	}

	attachmentID := uuid.New()
	var sha *string
	if s := strings.TrimSpace(req.Sha256); s != "" {
		sha = &s
	}
	_, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO challenge_attachments
		 (id, challenge_id, uploaded_by, filename, file_size, content_type, url, sha256, description, sort_order, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
		attachmentID, challengeID, uploaderUID, req.Name, fileSize, contentType, req.URL, sha, req.Description, req.SortOrder,
	)
	if err != nil {
		h.logger.Error("failed to save external attachment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save attachment"})
		return
	}
	h.logger.Info("external attachment added",
		zap.String("challenge_id", challengeID), zap.String("attachment_id", attachmentID.String()),
		zap.String("filename", req.Name), zap.String("url", req.URL))
	c.JSON(http.StatusCreated, gin.H{
		"id": attachmentID.String(), "filename": req.Name, "file_size": fileSize,
		"content_type": contentType, "url": req.URL, "sha256": req.Sha256, "sort_order": req.SortOrder,
	})
}

// headMeta does a short HEAD to learn an external file's size + content-type.
func headMeta(ctx context.Context, rawurl string) (contentType string, size int64, ok bool) {
	reqctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqctx, http.MethodHead, rawurl, nil)
	if err != nil {
		return "", 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false
	}
	return resp.Header.Get("Content-Type"), resp.ContentLength, true
}

// GET /api/v1/admin/challenges/:id/attachments
func (h *AttachmentHandler) List(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	var exists bool
	if err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM challenges WHERE id = $1)`, challengeID,
	).Scan(&exists); err != nil {
		h.logger.Error("failed to query challenge for attachment list", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch attachments"})
		return
	} else if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	attachments, err := h.queryAttachments(c, challengeID)
	if err != nil {
		h.logger.Error("failed to list attachments", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch attachments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"attachments": attachments})
}

// DELETE /api/v1/admin/challenges/:id/attachments/:attachment_id
func (h *AttachmentHandler) Delete(c *gin.Context) {
	challengeID := c.Param("id")
	attachmentID := c.Param("attachment_id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	if _, err := uuid.Parse(attachmentID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment ID"})
		return
	}
	if h.storageSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "attachment storage is unavailable"})
		return
	}

	var storageKey string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`DELETE FROM challenge_attachments WHERE id = $1 AND challenge_id = $2 RETURNING storage_key`,
		attachmentID, challengeID,
	).Scan(&storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to delete attachment metadata", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete attachment"})
		return
	}

	// metadata is removed first: if object deletion fails, the unreachable file
	// can be cleaned up later without leaving a public record that cannot download.
	if err := h.storageSvc.Delete(c.Request.Context(), storageKey); err != nil {
		h.logger.Warn("failed to delete attachment file", zap.Error(err), zap.String("key", storageKey))
	}

	c.JSON(http.StatusOK, gin.H{"message": "attachment deleted"})
}

// GET /api/v1/challenges/:slug/attachments/:attachment_id/download
func (h *AttachmentHandler) Download(c *gin.Context) {
	slug := c.Param("slug")
	attachmentID := c.Param("attachment_id")
	if _, err := uuid.Parse(attachmentID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment ID"})
		return
	}
	// look up attachment (join with challenge to validate slug ownership and published status)
	var storageKey, filename, contentType, externalURL string
	var fileSize int64
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(ca.storage_key, ''), ca.filename, COALESCE(ca.content_type, 'application/octet-stream'), ca.file_size, COALESCE(ca.url, '')
		 FROM challenge_attachments ca
		 JOIN challenges c ON c.id = ca.challenge_id
		 WHERE ca.id = $1 AND c.slug = $2 AND c.status = 'published'
		   AND (c.release_date IS NULL OR c.release_date <= NOW())`,
		attachmentID, slug,
	).Scan(&storageKey, &filename, &contentType, &fileSize, &externalURL)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query attachment for download", zap.String("attachment_id", attachmentID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}

	// external handout: the file lives on the public bucket, not our storage backend.
	// redirect the player straight to it (no storage access needed).
	if externalURL != "" {
		c.Redirect(http.StatusFound, externalURL)
		return
	}

	if h.storageSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "attachment storage is unavailable"})
		return
	}
	if fileSize < 0 {
		h.logger.Error("attachment has invalid negative size", zap.String("attachment_id", attachmentID), zap.Int64("file_size", fileSize))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}
	if parsedType, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		contentType = parsedType
	} else {
		contentType = "application/octet-stream"
	}
	actualSize, err := h.storageSvc.GetSize(c.Request.Context(), storageKey)
	if err != nil {
		h.logger.Error("failed to inspect attachment", zap.Error(err), zap.String("key", storageKey))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}
	if actualSize != fileSize {
		h.logger.Error("attachment size does not match metadata", zap.String("key", storageKey), zap.Int64("expected", fileSize), zap.Int64("actual", actualSize))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}

	reader, err := h.storageSvc.Download(c.Request.Context(), storageKey)
	if err != nil {
		h.logger.Error("failed to open attachment", zap.Error(err), zap.String("key", storageKey))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file unavailable"})
		return
	}
	defer reader.Close()

	// force download with original filename; never render inline to prevent xss
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

func (h *AttachmentHandler) ListPublic(c *gin.Context, challengeID string) ([]AttachmentResponse, error) {
	return h.queryAttachments(c, challengeID)
}

func (h *AttachmentHandler) queryAttachments(c *gin.Context, challengeID string) ([]AttachmentResponse, error) {
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, filename, file_size, COALESCE(content_type, ''), COALESCE(description, ''), sort_order, created_at,
		        COALESCE(url, ''), COALESCE(sha256, '')
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
		if err := rows.Scan(&a.ID, &a.Filename, &a.FileSize, &a.ContentType, &a.Description, &a.SortOrder, &createdAt, &a.URL, &a.Sha256); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		if a.FileSize < 0 {
			return nil, fmt.Errorf("attachment %s has a negative file size", a.ID)
		}
		a.CreatedAt = createdAt.Unix()
		attachments = append(attachments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attachments: %w", err)
	}
	if attachments == nil {
		attachments = []AttachmentResponse{}
	}
	return attachments, nil
}
