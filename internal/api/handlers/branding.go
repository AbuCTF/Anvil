package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const maxBrandLogoBytes = 2 << 20

type BrandingHandler struct {
	db      *database.DB
	storage storage.StorageBackend
	logger  *zap.Logger
}

func NewBrandingHandler(db *database.DB, store storage.StorageBackend, logger *zap.Logger) *BrandingHandler {
	return &BrandingHandler{db: db, storage: store, logger: logger}
}

func (h *BrandingHandler) Logo(c *gin.Context) {
	var key, mime string
	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'branding.logo_key'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'branding.logo_mime'), '')
		FROM platform_settings
		WHERE key IN ('branding.logo_key', 'branding.logo_mime')
	`).Scan(&key, &mime)
	if err != nil || key == "" {
		c.Status(http.StatusNotFound)
		return
	}
	if h.storage == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	reader, err := h.storage.Download(c.Request.Context(), key)
	if err != nil {
		h.logger.Error("load event logo", zap.Error(err))
		c.Status(http.StatusNotFound)
		return
	}
	defer reader.Close()
	size, err := h.storage.GetSize(c.Request.Context(), key)
	if err != nil {
		h.logger.Error("read event logo size", zap.Error(err))
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.DataFromReader(http.StatusOK, size, mime, reader, nil)
}

func (h *BrandingHandler) UploadLogo(c *gin.Context) {
	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "branding storage is unavailable"})
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBrandLogoBytes+(64<<10))
	header, err := c.FormFile("logo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "choose a PNG or JPEG logo"})
		return
	}
	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read logo"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBrandLogoBytes+1))
	file.Close()
	if err != nil || len(data) == 0 || len(data) > maxBrandLogoBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "logo must be between 1 byte and 2 MB"})
		return
	}
	mime := http.DetectContentType(data)
	if mime != "image/png" && mime != "image/jpeg" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "logo must be a PNG or JPEG"})
		return
	}
	imageConfig, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || imageConfig.Width < 16 || imageConfig.Height < 16 || imageConfig.Width > 4096 || imageConfig.Height > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "logo dimensions must be between 16 and 4096 pixels"})
		return
	}
	key := "branding/logos/" + uuid.NewString()
	if err := h.storage.Upload(c.Request.Context(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		h.logger.Error("store event logo", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store logo"})
		return
	}
	removeNew := true
	defer func() {
		if removeNew {
			_ = h.storage.Delete(c.Request.Context(), key)
		}
	}()

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save logo"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var previous string
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT value #>> '{}' FROM platform_settings WHERE key = 'branding.logo_key' FOR UPDATE
	`).Scan(&previous); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save logo"})
		return
	}
	for setting, value := range map[string]string{"branding.logo_key": key, "branding.logo_mime": mime} {
		result, err := tx.Exec(c.Request.Context(), `
			UPDATE platform_settings SET value = to_jsonb($2::text), updated_at = NOW(), updated_by = $3 WHERE key = $1
		`, setting, value, uid)
		if err != nil || result.RowsAffected() != 1 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save logo"})
			return
		}
	}
	metadata, _ := json.Marshal(gin.H{"mime": mime, "width": imageConfig.Width, "height": imageConfig.Height, "size": len(data)})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, new_values, ip_address, user_agent)
		VALUES ($1, 'branding_logo_updated', 'platform_settings', $2::jsonb, $3, $4)
	`, uid, metadata, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save logo"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save logo"})
		return
	}
	removeNew = false
	if previous != "" {
		if err := h.storage.Delete(c.Request.Context(), previous); err != nil {
			h.logger.Warn("delete previous event logo", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"logo_url":  fmt.Sprintf("/api/v1/branding/logo?v=%s", strings.TrimPrefix(key, "branding/logos/")),
		"logo_key":  key,
		"logo_mime": mime,
	})
}

func (h *BrandingHandler) DeleteLogo(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove logo"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var key string
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT value #>> '{}' FROM platform_settings WHERE key = 'branding.logo_key' FOR UPDATE
	`).Scan(&key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove logo"})
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE platform_settings SET value = '""'::jsonb, updated_at = NOW(), updated_by = $1
		WHERE key IN ('branding.logo_key', 'branding.logo_mime')
	`, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove logo"})
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, new_values, ip_address, user_agent)
		VALUES ($1, 'branding_logo_removed', 'platform_settings', '{}'::jsonb, $2, $3)
	`, uid, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove logo"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove logo"})
		return
	}
	if key != "" && h.storage != nil {
		if err := h.storage.Delete(c.Request.Context(), key); err != nil {
			h.logger.Warn("delete event logo", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
