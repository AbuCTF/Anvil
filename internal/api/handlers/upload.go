package handlers

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/anvil-lab/anvil/internal/services/upload"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UploadHandler handles file upload operations
type UploadHandler struct {
	uploadService *upload.Service
	logger        *zap.Logger
}

// NewUploadHandler creates a new upload handler
func NewUploadHandler(uploadService *upload.Service, logger *zap.Logger) *UploadHandler {
	return &UploadHandler{
		uploadService: uploadService,
		logger:        logger,
	}
}

// InitUploadRequest represents the request to initialize an upload
type InitUploadRequest struct {
	Filename    string           `json:"filename" binding:"required"`
	FileType    upload.FileType  `json:"file_type" binding:"required"`
	TotalSize   int64            `json:"total_size" binding:"required,gt=0"`
	ContentType string           `json:"content_type"`
	ChunkSize   int64            `json:"chunk_size"`
	Checksum    string           `json:"checksum"`
	ChallengeID *string          `json:"challenge_id"`
}

// InitUploadResponse is returned when an upload is initialized
type InitUploadResponse struct {
	UploadID    string `json:"upload_id"`
	ChunkSize   int64  `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
	ExpiresAt   string `json:"expires_at"`
}

// InitUpload initializes a new chunked upload
// POST /api/v1/uploads
func (h *UploadHandler) InitUpload(c *gin.Context) {
	var req InitUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user ID from context
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Validate file type info
	typeInfo, ok := upload.GetFileTypeInfo(req.FileType)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
		return
	}

	// Check size limits
	if req.TotalSize > typeInfo.MaxSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "file too large",
			"max_size": typeInfo.MaxSize,
		})
		return
	}

	// Initialize upload
	uploadReq := upload.InitUploadRequest{
		Filename:    req.Filename,
		FileType:    req.FileType,
		TotalSize:   req.TotalSize,
		ContentType: req.ContentType,
		ChunkSize:   req.ChunkSize,
		Checksum:    req.Checksum,
		ChallengeID: req.ChallengeID,
	}

	uploadSession, err := h.uploadService.InitUpload(c.Request.Context(), userID.(string), uploadReq)
	if err != nil {
		h.logger.Error("failed to initialize upload", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, InitUploadResponse{
		UploadID:    uploadSession.ID,
		ChunkSize:   uploadSession.ChunkSize,
		TotalChunks: uploadSession.TotalChunks,
		ExpiresAt:   uploadSession.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// UploadChunk handles uploading a single chunk
// PUT /api/v1/uploads/:id/chunks/:number
func (h *UploadHandler) UploadChunk(c *gin.Context) {
	uploadID := c.Param("id")
	chunkNumberStr := c.Param("number")

	chunkNumber, err := strconv.Atoi(chunkNumberStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk number"})
		return
	}

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Verify upload belongs to user
	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Get content length
	contentLength := c.Request.ContentLength
	if contentLength <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content-length required"})
		return
	}

	// Upload the chunk
	if err := h.uploadService.UploadChunk(
		c.Request.Context(),
		uploadID,
		chunkNumber,
		c.Request.Body,
		contentLength,
	); err != nil {
		h.logger.Error("failed to upload chunk",
			zap.String("upload_id", uploadID),
			zap.Int("chunk", chunkNumber),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chunk":   chunkNumber,
		"message": "chunk uploaded successfully",
	})
}

// CompleteUpload finalizes a chunked upload
// POST /api/v1/uploads/:id/complete
func (h *UploadHandler) CompleteUpload(c *gin.Context) {
	uploadID := c.Param("id")

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Verify upload belongs to user
	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Complete the upload
	completed, err := h.uploadService.CompleteUpload(c.Request.Context(), uploadID)
	if err != nil {
		h.logger.Error("failed to complete upload",
			zap.String("upload_id", uploadID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_id":   completed.ID,
		"status":      completed.Status,
		"storage_key": completed.StorageKey,
		"total_size":  completed.TotalSize,
		"message":     "upload completed successfully",
	})
}

// GetUploadStatus returns the current status of an upload
// GET /api/v1/uploads/:id
func (h *UploadHandler) GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("id")

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	c.JSON(http.StatusOK, uploadSession)
}

// GetUploadProgress returns detailed progress info
// GET /api/v1/uploads/:id/progress
func (h *UploadHandler) GetUploadProgress(c *gin.Context) {
	uploadID := c.Param("id")

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	progress, err := h.uploadService.GetProgress(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, progress)
}

// GetMissingChunks returns which chunks still need to be uploaded
// GET /api/v1/uploads/:id/missing
func (h *UploadHandler) GetMissingChunks(c *gin.Context) {
	uploadID := c.Param("id")

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	missing, err := h.uploadService.GetMissingChunks(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"missing_chunks": missing,
		"count":          len(missing),
	})
}

// CancelUpload cancels an in-progress upload
// DELETE /api/v1/uploads/:id
func (h *UploadHandler) CancelUpload(c *gin.Context) {
	uploadID := c.Param("id")

	// Get user ID and verify ownership
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if uploadSession.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.uploadService.CancelUpload(c.Request.Context(), uploadID); err != nil {
		h.logger.Error("failed to cancel upload",
			zap.String("upload_id", uploadID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "upload cancelled"})
}

// ListUserUploads lists all uploads for the current user
// GET /api/v1/uploads
func (h *UploadHandler) ListUserUploads(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploads, err := h.uploadService.GetUserUploads(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"uploads": uploads,
		"count":   len(uploads),
	})
}

// SimpleUpload handles small file uploads without chunking
// POST /api/v1/uploads/simple
func (h *UploadHandler) SimpleUpload(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Parse multipart form (max 100MB for simple uploads)
	if err := c.Request.ParseMultipartForm(100 * 1024 * 1024); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse form"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	defer file.Close()

	fileTypeStr := c.PostForm("file_type")
	if fileTypeStr == "" {
		// Try to detect from filename
		fileTypeStr = string(upload.DetectFileType(header.Filename, header.Header.Get("Content-Type")))
	}

	fileType := upload.FileType(fileTypeStr)
	typeInfo, ok := upload.GetFileTypeInfo(fileType)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
		return
	}

	// For simple upload, enforce smaller limit
	maxSimpleSize := int64(100 * 1024 * 1024) // 100MB
	if typeInfo.MaxSize < maxSimpleSize {
		maxSimpleSize = typeInfo.MaxSize
	}
	if header.Size > maxSimpleSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "file too large for simple upload, use chunked upload",
			"max_size": maxSimpleSize,
		})
		return
	}

	challengeID := c.PostForm("challenge_id")
	var challengeIDPtr *string
	if challengeID != "" {
		challengeIDPtr = &challengeID
	}

	// Initialize and complete upload in one go
	uploadReq := upload.InitUploadRequest{
		Filename:    header.Filename,
		FileType:    fileType,
		TotalSize:   header.Size,
		ContentType: header.Header.Get("Content-Type"),
		ChunkSize:   header.Size, // Single chunk
		ChallengeID: challengeIDPtr,
	}

	uploadSession, err := h.uploadService.InitUpload(c.Request.Context(), userID.(string), uploadReq)
	if err != nil {
		h.logger.Error("failed to initialize simple upload", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Upload as single chunk
	if err := h.uploadService.UploadChunk(c.Request.Context(), uploadSession.ID, 1, file, header.Size); err != nil {
		h.uploadService.CancelUpload(c.Request.Context(), uploadSession.ID)
		h.logger.Error("failed to upload file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Complete upload
	completed, err := h.uploadService.CompleteUpload(c.Request.Context(), uploadSession.ID)
	if err != nil {
		h.logger.Error("failed to complete simple upload", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_id":   completed.ID,
		"status":      completed.Status,
		"storage_key": completed.StorageKey,
		"filename":    completed.Filename,
		"size":        completed.TotalSize,
	})
}

// GetSupportedTypes returns information about supported file types
// GET /api/v1/uploads/types
func (h *UploadHandler) GetSupportedTypes(c *gin.Context) {
	types := []gin.H{
		{
			"type":       "dockerfile",
			"extensions": []string{"Dockerfile", "dockerfile"},
			"max_size":   1 * 1024 * 1024,
			"description": "Dockerfile for building container images",
		},
		{
			"type":       "docker_context",
			"extensions": []string{".tar.gz", ".tgz", ".tar"},
			"max_size":   500 * 1024 * 1024,
			"description": "Docker build context archive",
		},
		{
			"type":       "docker_image",
			"extensions": []string{".tar"},
			"max_size":   10 * 1024 * 1024 * 1024,
			"description": "Exported Docker image",
		},
		{
			"type":       "ova",
			"extensions": []string{".ova"},
			"max_size":   50 * 1024 * 1024 * 1024,
			"description": "Open Virtual Appliance (VirtualBox/VMware)",
		},
		{
			"type":       "vmdk",
			"extensions": []string{".vmdk"},
			"max_size":   50 * 1024 * 1024 * 1024,
			"description": "VMware Virtual Disk",
		},
		{
			"type":       "qcow2",
			"extensions": []string{".qcow2", ".qcow"},
			"max_size":   50 * 1024 * 1024 * 1024,
			"description": "QEMU Copy-On-Write disk image",
		},
		{
			"type":       "iso",
			"extensions": []string{".iso"},
			"max_size":   10 * 1024 * 1024 * 1024,
			"description": "ISO disk image",
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"types":           types,
		"chunk_size":      10 * 1024 * 1024,
		"max_chunk_size":  100 * 1024 * 1024,
		"simple_upload_max": 100 * 1024 * 1024,
	})
}

// Helper to format bytes
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// Unused import fix
var _ = io.Copy
