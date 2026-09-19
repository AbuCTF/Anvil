package handlers

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/services/upload"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	minimumUploadChunkSize = int64(1 << 20)
	maximumUploadChunkSize = int64(100 << 20)
	maximumSimpleUpload    = int64(100 << 20)
	maximumSimpleRequest   = maximumSimpleUpload + (1 << 20)
	maximumUploadFilename  = 200
	maximumUploadInitBody  = int64(64 << 10)
)

type UploadHandler struct {
	uploadService *upload.Service
	logger        *zap.Logger
}

func NewUploadHandler(uploadService *upload.Service, logger *zap.Logger) *UploadHandler {
	return &UploadHandler{
		uploadService: uploadService,
		logger:        logger,
	}
}

type InitUploadRequest struct {
	Filename    string          `json:"filename" binding:"required"`
	FileType    upload.FileType `json:"file_type" binding:"required"`
	TotalSize   int64           `json:"total_size" binding:"required,gt=0"`
	ContentType string          `json:"content_type"`
	ChunkSize   int64           `json:"chunk_size"`
	Checksum    string          `json:"checksum"`
	ChallengeID *string         `json:"challenge_id"`
}

type InitUploadResponse struct {
	UploadID    string `json:"upload_id"`
	ChunkSize   int64  `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
	ExpiresAt   string `json:"expires_at"`
}

type UploadResponse struct {
	ID             string              `json:"id"`
	ChallengeID    *string             `json:"challenge_id,omitempty"`
	Filename       string              `json:"filename"`
	FileType       upload.FileType     `json:"file_type"`
	ContentType    string              `json:"content_type,omitempty"`
	TotalSize      int64               `json:"total_size"`
	UploadedSize   int64               `json:"uploaded_size"`
	ChunkSize      int64               `json:"chunk_size"`
	TotalChunks    int                 `json:"total_chunks"`
	UploadedChunks int                 `json:"uploaded_chunks"`
	Status         upload.UploadStatus `json:"status"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	ExpiresAt      string              `json:"expires_at"`
}

func publicUpload(uploadSession *upload.Upload) UploadResponse {
	return UploadResponse{
		ID:             uploadSession.ID,
		ChallengeID:    uploadSession.ChallengeID,
		Filename:       uploadSession.Filename,
		FileType:       uploadSession.FileType,
		ContentType:    uploadSession.ContentType,
		TotalSize:      uploadSession.TotalSize,
		UploadedSize:   uploadSession.UploadedSize,
		ChunkSize:      uploadSession.ChunkSize,
		TotalChunks:    uploadSession.TotalChunks,
		UploadedChunks: len(uploadSession.UploadedChunks),
		Status:         uploadSession.Status,
		CreatedAt:      uploadSession.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      uploadSession.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt:      uploadSession.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func normalizeUploadFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "" || filename == "." || filename == ".." || filename == "/" {
		return "", errors.New("filename is required")
	}
	if len(filename) > maximumUploadFilename {
		return "", fmt.Errorf("filename must be at most %d bytes", maximumUploadFilename)
	}
	if !utf8.ValidString(filename) || strings.IndexFunc(filename, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", errors.New("filename contains invalid characters")
	}
	return filename, nil
}

func normalizeUploadChecksum(checksum string) (string, error) {
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	if checksum == "" {
		return "", nil
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("checksum must be a 64-character SHA-256 hex digest")
	}
	return checksum, nil
}

func normalizeUploadChallengeID(challengeID *string) (*string, error) {
	if challengeID == nil || strings.TrimSpace(*challengeID) == "" {
		return nil, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*challengeID))
	if err != nil {
		return nil, errors.New("invalid challenge_id")
	}
	canonical := id.String()
	return &canonical, nil
}

func supportedUploadType(fileType upload.FileType) bool {
	switch fileType {
	case upload.FileTypeDockerfile, upload.FileTypeDockerContext, upload.FileTypeDockerImage,
		upload.FileTypeOVA, upload.FileTypeVMDK, upload.FileTypeQCOW2:
		return true
	default:
		return false
	}
}

func expectedUploadChunkSize(uploadSession *upload.Upload, chunkNumber int) (int64, error) {
	if uploadSession == nil || uploadSession.TotalChunks < 1 || uploadSession.ChunkSize < 1 || uploadSession.TotalSize < 1 {
		return 0, errors.New("invalid upload session")
	}
	if chunkNumber < 1 || chunkNumber > uploadSession.TotalChunks {
		return 0, errors.New("invalid chunk number")
	}
	if chunkNumber < uploadSession.TotalChunks {
		return uploadSession.ChunkSize, nil
	}
	lastSize := uploadSession.TotalSize - int64(chunkNumber-1)*uploadSession.ChunkSize
	if lastSize < 1 || lastSize > uploadSession.ChunkSize {
		return 0, errors.New("invalid final chunk size")
	}
	return lastSize, nil
}

func (h *UploadHandler) requireService(c *gin.Context) bool {
	if h.uploadService != nil {
		return true
	}
	h.logger.Error("upload service unavailable")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "upload service unavailable"})
	return false
}

func (h *UploadHandler) ownedUpload(c *gin.Context, uploadID string) (string, *upload.Upload, bool) {
	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return "", nil, false
	}
	if !h.requireService(c) {
		return "", nil, false
	}
	id, err := uuid.Parse(uploadID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload id"})
		return "", nil, false
	}
	canonicalID := id.String()
	uploadSession, err := h.uploadService.GetUpload(c.Request.Context(), canonicalID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return "", nil, false
	}
	if uploadSession == nil || uploadSession.UserID != userID.String() {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return "", nil, false
	}
	return canonicalID, uploadSession, true
}

// POST /api/v1/uploads
func (h *UploadHandler) InitUpload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumUploadInitBody)
	var req InitUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "upload request is too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload request"})
		return
	}

	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !h.requireService(c) {
		return
	}

	req.FileType = upload.FileType(strings.ToLower(strings.TrimSpace(string(req.FileType))))
	typeInfo, ok := upload.GetFileTypeInfo(req.FileType)
	if !ok || !supportedUploadType(req.FileType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
		return
	}

	if req.TotalSize > typeInfo.MaxSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "file too large",
			"max_size": typeInfo.MaxSize,
		})
		return
	}
	filename, err := normalizeUploadFilename(req.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ChunkSize != 0 && (req.ChunkSize < minimumUploadChunkSize || req.ChunkSize > maximumUploadChunkSize) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          "chunk_size is outside the supported range",
			"min_chunk_size": minimumUploadChunkSize,
			"max_chunk_size": maximumUploadChunkSize,
		})
		return
	}
	checksum, err := normalizeUploadChecksum(req.Checksum)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	challengeID, err := normalizeUploadChallengeID(req.ChallengeID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	contentType := strings.TrimSpace(req.ContentType)
	if len(contentType) > 200 || strings.IndexFunc(contentType, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid content_type"})
		return
	}

	uploadReq := upload.InitUploadRequest{
		Filename:    filename,
		FileType:    req.FileType,
		TotalSize:   req.TotalSize,
		ContentType: contentType,
		ChunkSize:   req.ChunkSize,
		Checksum:    checksum,
		ChallengeID: challengeID,
	}

	uploadSession, err := h.uploadService.InitUpload(c.Request.Context(), userID.String(), uploadReq)
	if err != nil {
		h.logger.Error("failed to initialize upload", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
		return
	}
	if uploadSession == nil {
		h.logger.Error("upload service returned an empty successful initialization")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
		return
	}

	c.JSON(http.StatusCreated, InitUploadResponse{
		UploadID:    uploadSession.ID,
		ChunkSize:   uploadSession.ChunkSize,
		TotalChunks: uploadSession.TotalChunks,
		ExpiresAt:   uploadSession.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// PUT /api/v1/uploads/:id/chunks/:number
func (h *UploadHandler) UploadChunk(c *gin.Context) {
	chunkNumberStr := c.Param("number")

	chunkNumber, err := strconv.Atoi(chunkNumberStr)
	if err != nil || chunkNumber < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk number"})
		return
	}

	uploadID, uploadSession, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}
	if chunkNumber > uploadSession.TotalChunks {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk number exceeds total chunks"})
		return
	}
	if uploadSession.Status != upload.UploadStatusPending && uploadSession.Status != upload.UploadStatusUploading {
		c.JSON(http.StatusConflict, gin.H{"error": "upload does not accept chunks in its current state"})
		return
	}

	contentLength := c.Request.ContentLength
	if contentLength <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content-length required"})
		return
	}
	expectedLength, err := expectedUploadChunkSize(uploadSession, chunkNumber)
	if err != nil {
		h.logger.Error("upload session has inconsistent chunk metadata", zap.String("upload_id", uploadID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid upload state"})
		return
	}
	if contentLength != expectedLength {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":         "invalid chunk size",
			"expected_size": expectedLength,
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, expectedLength)

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload chunk"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chunk":   chunkNumber,
		"message": "chunk uploaded successfully",
	})
}

// POST /api/v1/uploads/:id/complete
func (h *UploadHandler) CompleteUpload(c *gin.Context) {
	uploadID, uploadSession, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}
	if uploadSession.Status == upload.UploadStatusCompleted {
		c.JSON(http.StatusOK, gin.H{
			"upload_id":   uploadSession.ID,
			"status":      uploadSession.Status,
			"storage_key": uploadSession.StorageKey,
			"total_size":  uploadSession.TotalSize,
			"message":     "upload already completed",
		})
		return
	}
	if uploadSession.Status == upload.UploadStatusFailed || uploadSession.Status == upload.UploadStatusCancelled {
		c.JSON(http.StatusConflict, gin.H{"error": "upload cannot be completed in its current state"})
		return
	}
	if len(uploadSession.UploadedChunks) != uploadSession.TotalChunks {
		c.JSON(http.StatusConflict, gin.H{
			"error":           "upload is incomplete",
			"uploaded_chunks": len(uploadSession.UploadedChunks),
			"total_chunks":    uploadSession.TotalChunks,
		})
		return
	}

	completed, err := h.uploadService.CompleteUpload(c.Request.Context(), uploadID)
	if err != nil {
		h.logger.Error("failed to complete upload",
			zap.String("upload_id", uploadID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete upload"})
		return
	}
	if completed == nil {
		h.logger.Error("upload service returned an empty successful completion", zap.String("upload_id", uploadID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete upload"})
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

// GET /api/v1/uploads/:id
func (h *UploadHandler) GetUploadStatus(c *gin.Context) {
	_, uploadSession, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}

	c.JSON(http.StatusOK, publicUpload(uploadSession))
}

// GET /api/v1/uploads/:id/progress
func (h *UploadHandler) GetUploadProgress(c *gin.Context) {
	uploadID, _, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}

	progress, err := h.uploadService.GetProgress(c.Request.Context(), uploadID)
	if err != nil {
		h.logger.Error("failed to get upload progress", zap.String("upload_id", uploadID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get upload progress"})
		return
	}
	if progress == nil {
		h.logger.Error("upload service returned empty progress", zap.String("upload_id", uploadID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get upload progress"})
		return
	}

	c.JSON(http.StatusOK, progress)
}

// GET /api/v1/uploads/:id/missing
func (h *UploadHandler) GetMissingChunks(c *gin.Context) {
	uploadID, _, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}

	missing, err := h.uploadService.GetMissingChunks(c.Request.Context(), uploadID)
	if err != nil {
		h.logger.Error("failed to get missing upload chunks", zap.String("upload_id", uploadID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get missing chunks"})
		return
	}
	if missing == nil {
		missing = []int{}
	}

	c.JSON(http.StatusOK, gin.H{
		"missing_chunks": missing,
		"count":          len(missing),
	})
}

// DELETE /api/v1/uploads/:id
func (h *UploadHandler) CancelUpload(c *gin.Context) {
	uploadID, uploadSession, ok := h.ownedUpload(c, c.Param("id"))
	if !ok {
		return
	}
	if uploadSession.Status == upload.UploadStatusCompleted {
		c.JSON(http.StatusConflict, gin.H{"error": "completed uploads cannot be cancelled"})
		return
	}
	if uploadSession.Status == upload.UploadStatusCancelled {
		c.JSON(http.StatusOK, gin.H{"message": "upload already cancelled"})
		return
	}

	if err := h.uploadService.CancelUpload(c.Request.Context(), uploadID); err != nil {
		h.logger.Error("failed to cancel upload",
			zap.String("upload_id", uploadID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel upload"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "upload cancelled"})
}

// GET /api/v1/uploads
func (h *UploadHandler) ListUserUploads(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !h.requireService(c) {
		return
	}

	uploads, err := h.uploadService.GetUserUploads(c.Request.Context(), userID.String())
	if err != nil {
		h.logger.Error("failed to list user uploads", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list uploads"})
		return
	}
	responses := make([]UploadResponse, 0, len(uploads))
	for _, uploadSession := range uploads {
		if uploadSession == nil {
			h.logger.Error("upload service returned a nil session while listing", zap.String("user_id", userID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list uploads"})
			return
		}
		responses = append(responses, publicUpload(uploadSession))
	}

	c.JSON(http.StatusOK, gin.H{
		"uploads": responses,
		"count":   len(responses),
	})
}

// POST /api/v1/uploads/simple
func (h *UploadHandler) SimpleUpload(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !h.requireService(c) {
		return
	}

	// bound the whole multipart request before parsing so oversized uploads don't spill arbitrary form data to temporary disk
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumSimpleRequest)
	if err := c.Request.ParseMultipartForm(16 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "simple upload request is too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse form"})
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() {
			if err := c.Request.MultipartForm.RemoveAll(); err != nil {
				h.logger.Warn("failed to clean multipart temporary files", zap.Error(err))
			}
		}()
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.logger.Warn("failed to close simple upload stream", zap.Error(err))
		}
	}()
	filename, err := normalizeUploadFilename(header.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if header.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file must not be empty"})
		return
	}

	fileTypeStr := c.PostForm("file_type")
	if fileTypeStr == "" {
		fileTypeStr = string(upload.DetectFileType(header.Filename, header.Header.Get("Content-Type")))
	}

	fileType := upload.FileType(strings.ToLower(strings.TrimSpace(fileTypeStr)))
	typeInfo, ok := upload.GetFileTypeInfo(fileType)
	if !ok || !supportedUploadType(fileType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
		return
	}

	maxSimpleSize := maximumSimpleUpload
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
	challengeIDPtr, err = normalizeUploadChallengeID(challengeIDPtr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if len(contentType) > 200 || strings.IndexFunc(contentType, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid content type"})
		return
	}

	uploadReq := upload.InitUploadRequest{
		Filename:    filename,
		FileType:    fileType,
		TotalSize:   header.Size,
		ContentType: contentType,
		ChunkSize:   header.Size,
		ChallengeID: challengeIDPtr,
	}

	uploadSession, err := h.uploadService.InitUpload(c.Request.Context(), userID.String(), uploadReq)
	if err != nil {
		h.logger.Error("failed to initialize simple upload", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
		return
	}
	if uploadSession == nil {
		h.logger.Error("upload service returned an empty successful simple initialization")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
		return
	}

	if err := h.uploadService.UploadChunk(c.Request.Context(), uploadSession.ID, 1, file, header.Size); err != nil {
		if cleanupErr := h.uploadService.CancelUpload(c.Request.Context(), uploadSession.ID); cleanupErr != nil {
			h.logger.Error("failed to cancel incomplete simple upload", zap.String("upload_id", uploadSession.ID), zap.Error(cleanupErr))
		}
		h.logger.Error("failed to upload file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}

	completed, err := h.uploadService.CompleteUpload(c.Request.Context(), uploadSession.ID)
	if err != nil {
		h.logger.Error("failed to complete simple upload", zap.Error(err))
		if cleanupErr := h.uploadService.CancelUpload(c.Request.Context(), uploadSession.ID); cleanupErr != nil {
			h.logger.Error("failed to clean up incomplete simple upload", zap.String("upload_id", uploadSession.ID), zap.Error(cleanupErr))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete upload"})
		return
	}
	if completed == nil {
		h.logger.Error("upload service returned an empty successful simple completion", zap.String("upload_id", uploadSession.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete upload"})
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

// GET /api/v1/uploads/types
func (h *UploadHandler) GetSupportedTypes(c *gin.Context) {
	descriptions := map[upload.FileType]string{
		upload.FileTypeDockerfile:    "Dockerfile for building container images",
		upload.FileTypeDockerContext: "Docker build context archive",
		upload.FileTypeDockerImage:   "Exported Docker image",
		upload.FileTypeOVA:           "Open Virtual Appliance (VirtualBox/VMware)",
		upload.FileTypeVMDK:          "VMware Virtual Disk",
		upload.FileTypeQCOW2:         "QEMU Copy-On-Write disk image",
	}
	orderedTypes := []upload.FileType{
		upload.FileTypeDockerfile,
		upload.FileTypeDockerContext,
		upload.FileTypeDockerImage,
		upload.FileTypeOVA,
		upload.FileTypeVMDK,
		upload.FileTypeQCOW2,
	}
	types := make([]gin.H, 0, len(orderedTypes))
	for _, fileType := range orderedTypes {
		info, ok := upload.GetFileTypeInfo(fileType)
		if !ok {
			h.logger.Error("configured upload type is missing registry metadata", zap.String("file_type", string(fileType)))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load supported upload types"})
			return
		}
		types = append(types, gin.H{
			"type":        fileType,
			"extensions":  info.Extensions,
			"max_size":    info.MaxSize,
			"description": descriptions[fileType],
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"types":             types,
		"chunk_size":        10 << 20,
		"min_chunk_size":    minimumUploadChunkSize,
		"max_chunk_size":    maximumUploadChunkSize,
		"simple_upload_max": maximumSimpleUpload,
	})
}

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
