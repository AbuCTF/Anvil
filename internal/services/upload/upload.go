// Package upload provides a high-level upload service for handling large file uploads
// with chunking, validation, and progress tracking.
package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// FileType represents supported file types
type FileType string

const (
	FileTypeDockerfile    FileType = "dockerfile"
	FileTypeDockerContext FileType = "docker_context" // tar.gz of build context
	FileTypeDockerImage   FileType = "docker_image"   // Exported docker image tar
	FileTypeOVA           FileType = "ova"
	FileTypeVMDK          FileType = "vmdk"
	FileTypeQCOW2         FileType = "qcow2"
	FileTypeVDI           FileType = "vdi"
	FileTypeISO           FileType = "iso"
	FileTypeUnknown       FileType = "unknown"
)

// FileTypeInfo contains metadata about a file type
type FileTypeInfo struct {
	Type         FileType
	MimeTypes    []string
	Extensions   []string
	MaxSize      int64 // Maximum allowed size in bytes
	RequiresAuth bool  // Whether upload requires authentication
	IsVM         bool  // Whether this is a VM image type
}

var fileTypeRegistry = map[FileType]FileTypeInfo{
	FileTypeDockerfile: {
		Type:         FileTypeDockerfile,
		MimeTypes:    []string{"text/plain", "application/octet-stream"},
		Extensions:   []string{"", "dockerfile", "Dockerfile"},
		MaxSize:      1 * 1024 * 1024, // 1MB max
		RequiresAuth: true,
		IsVM:         false,
	},
	FileTypeDockerContext: {
		Type:         FileTypeDockerContext,
		MimeTypes:    []string{"application/gzip", "application/x-tar", "application/x-gzip"},
		Extensions:   []string{".tar.gz", ".tgz", ".tar"},
		MaxSize:      500 * 1024 * 1024, // 500MB max
		RequiresAuth: true,
		IsVM:         false,
	},
	FileTypeDockerImage: {
		Type:         FileTypeDockerImage,
		MimeTypes:    []string{"application/x-tar"},
		Extensions:   []string{".tar"},
		MaxSize:      10 * 1024 * 1024 * 1024, // 10GB max
		RequiresAuth: true,
		IsVM:         false,
	},
	FileTypeOVA: {
		Type:         FileTypeOVA,
		MimeTypes:    []string{"application/x-tar", "application/ovf"},
		Extensions:   []string{".ova"},
		MaxSize:      50 * 1024 * 1024 * 1024, // 50GB max
		RequiresAuth: true,
		IsVM:         true,
	},
	FileTypeVMDK: {
		Type:         FileTypeVMDK,
		MimeTypes:    []string{"application/octet-stream", "application/x-vmdk"},
		Extensions:   []string{".vmdk"},
		MaxSize:      50 * 1024 * 1024 * 1024, // 50GB max
		RequiresAuth: true,
		IsVM:         true,
	},
	FileTypeQCOW2: {
		Type:         FileTypeQCOW2,
		MimeTypes:    []string{"application/octet-stream"},
		Extensions:   []string{".qcow2", ".qcow"},
		MaxSize:      50 * 1024 * 1024 * 1024, // 50GB max
		RequiresAuth: true,
		IsVM:         true,
	},
	FileTypeVDI: {
		Type:         FileTypeVDI,
		MimeTypes:    []string{"application/octet-stream"},
		Extensions:   []string{".vdi"},
		MaxSize:      50 * 1024 * 1024 * 1024, // 50GB max
		RequiresAuth: true,
		IsVM:         true,
	},
	FileTypeISO: {
		Type:         FileTypeISO,
		MimeTypes:    []string{"application/x-iso9660-image", "application/octet-stream"},
		Extensions:   []string{".iso"},
		MaxSize:      10 * 1024 * 1024 * 1024, // 10GB max
		RequiresAuth: true,
		IsVM:         true,
	},
}

// UploadStatus represents the current state of an upload
type UploadStatus string

const (
	UploadStatusPending    UploadStatus = "pending"
	UploadStatusUploading  UploadStatus = "uploading"
	UploadStatusProcessing UploadStatus = "processing"
	UploadStatusValidating UploadStatus = "validating"
	UploadStatusCompleted  UploadStatus = "completed"
	UploadStatusFailed     UploadStatus = "failed"
	UploadStatusCancelled  UploadStatus = "cancelled"
)

// Upload represents an upload session
type Upload struct {
	ID            string                   `json:"id"`
	UserID        string                   `json:"user_id"`
	ChallengeID   *string                  `json:"challenge_id,omitempty"`
	Filename      string                   `json:"filename"`
	FileType      FileType                 `json:"file_type"`
	ContentType   string                   `json:"content_type"`
	TotalSize     int64                    `json:"total_size"`
	UploadedSize  int64                    `json:"uploaded_size"`
	ChunkSize     int64                    `json:"chunk_size"`
	TotalChunks   int                      `json:"total_chunks"`
	UploadedChunks map[int]storage.CompletedPart `json:"uploaded_chunks"`
	Status        UploadStatus             `json:"status"`
	StorageKey    string                   `json:"storage_key"`
	BackendUploadID string                 `json:"backend_upload_id"`
	Checksum      string                   `json:"checksum"`
	Error         string                   `json:"error,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
	ExpiresAt     time.Time                `json:"expires_at"`
}

// UploadProgress contains progress information for an upload
type UploadProgress struct {
	UploadID       string       `json:"upload_id"`
	Status         UploadStatus `json:"status"`
	TotalSize      int64        `json:"total_size"`
	UploadedSize   int64        `json:"uploaded_size"`
	TotalChunks    int          `json:"total_chunks"`
	UploadedChunks int          `json:"uploaded_chunks"`
	PercentComplete float64     `json:"percent_complete"`
	BytesPerSecond  int64       `json:"bytes_per_second,omitempty"`
	EstimatedTimeRemaining int64 `json:"estimated_time_remaining,omitempty"`
}

// InitUploadRequest contains parameters for initializing an upload
type InitUploadRequest struct {
	Filename    string   `json:"filename" binding:"required"`
	FileType    FileType `json:"file_type" binding:"required"`
	TotalSize   int64    `json:"total_size" binding:"required,gt=0"`
	ContentType string   `json:"content_type"`
	ChunkSize   int64    `json:"chunk_size"` // Optional, will use default if not provided
	Checksum    string   `json:"checksum"`   // Optional SHA256 of the complete file
	ChallengeID *string  `json:"challenge_id"`
}

// Service handles file uploads
type Service struct {
	storage     storage.StorageBackend
	logger      *zap.Logger
	mu          sync.RWMutex
	uploads     map[string]*Upload
	config      Config
}

// Config contains upload service configuration
type Config struct {
	DefaultChunkSize     int64         // Default chunk size (e.g., 10MB)
	MinChunkSize         int64         // Minimum allowed chunk size
	MaxChunkSize         int64         // Maximum allowed chunk size
	UploadExpiry         time.Duration // How long incomplete uploads are kept
	MaxConcurrentUploads int           // Max concurrent uploads per user
	AllowedFileTypes     []FileType    // Which file types are allowed
}

// DefaultConfig returns sensible default configuration
func DefaultConfig() Config {
	return Config{
		DefaultChunkSize:     10 * 1024 * 1024,  // 10MB
		MinChunkSize:         1 * 1024 * 1024,   // 1MB
		MaxChunkSize:         100 * 1024 * 1024, // 100MB
		UploadExpiry:         24 * time.Hour,
		MaxConcurrentUploads: 3,
		AllowedFileTypes: []FileType{
			FileTypeDockerfile,
			FileTypeDockerContext,
			FileTypeDockerImage,
			FileTypeOVA,
			FileTypeVMDK,
			FileTypeQCOW2,
		},
	}
}

// NewService creates a new upload service
func NewService(storage storage.StorageBackend, logger *zap.Logger, config Config) *Service {
	return &Service{
		storage: storage,
		logger:  logger,
		uploads: make(map[string]*Upload),
		config:  config,
	}
}

// InitUpload initializes a new upload session
func (s *Service) InitUpload(ctx context.Context, userID string, req InitUploadRequest) (*Upload, error) {
	// Validate file type
	typeInfo, ok := fileTypeRegistry[req.FileType]
	if !ok {
		return nil, fmt.Errorf("unsupported file type: %s", req.FileType)
	}

	// Check if file type is allowed
	allowed := false
	for _, ft := range s.config.AllowedFileTypes {
		if ft == req.FileType {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("file type not allowed: %s", req.FileType)
	}

	// Validate size
	if req.TotalSize > typeInfo.MaxSize {
		return nil, fmt.Errorf("file too large: max size for %s is %d bytes", req.FileType, typeInfo.MaxSize)
	}

	// Determine chunk size
	chunkSize := req.ChunkSize
	if chunkSize == 0 {
		chunkSize = s.config.DefaultChunkSize
	}
	if chunkSize < s.config.MinChunkSize {
		chunkSize = s.config.MinChunkSize
	}
	if chunkSize > s.config.MaxChunkSize {
		chunkSize = s.config.MaxChunkSize
	}

	// Calculate total chunks
	totalChunks := int((req.TotalSize + chunkSize - 1) / chunkSize)

	// Generate upload ID and storage key
	uploadID := uuid.New().String()
	storageKey := generateStorageKey(userID, req.FileType, uploadID, req.Filename)

	// Initialize backend multipart upload
	backendUploadID, err := s.storage.InitMultipartUpload(ctx, storageKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage upload: %w", err)
	}

	upload := &Upload{
		ID:              uploadID,
		UserID:          userID,
		ChallengeID:     req.ChallengeID,
		Filename:        req.Filename,
		FileType:        req.FileType,
		ContentType:     req.ContentType,
		TotalSize:       req.TotalSize,
		UploadedSize:    0,
		ChunkSize:       chunkSize,
		TotalChunks:     totalChunks,
		UploadedChunks:  make(map[int]storage.CompletedPart),
		Status:          UploadStatusPending,
		StorageKey:      storageKey,
		BackendUploadID: backendUploadID,
		Checksum:        req.Checksum,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		ExpiresAt:       time.Now().Add(s.config.UploadExpiry),
	}

	s.mu.Lock()
	s.uploads[uploadID] = upload
	s.mu.Unlock()

	s.logger.Info("upload initialized",
		zap.String("upload_id", uploadID),
		zap.String("user_id", userID),
		zap.String("filename", req.Filename),
		zap.String("file_type", string(req.FileType)),
		zap.Int64("total_size", req.TotalSize),
		zap.Int("total_chunks", totalChunks),
	)

	return upload, nil
}

// UploadChunk handles uploading a single chunk
func (s *Service) UploadChunk(ctx context.Context, uploadID string, chunkNumber int, reader io.Reader, size int64) error {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", uploadID)
	}

	if upload.Status == UploadStatusCompleted || upload.Status == UploadStatusFailed || upload.Status == UploadStatusCancelled {
		return fmt.Errorf("upload is %s", upload.Status)
	}

	if chunkNumber < 1 || chunkNumber > upload.TotalChunks {
		return fmt.Errorf("invalid chunk number: %d (expected 1-%d)", chunkNumber, upload.TotalChunks)
	}

	// Check if chunk already uploaded
	s.mu.RLock()
	if _, uploaded := upload.UploadedChunks[chunkNumber]; uploaded {
		s.mu.RUnlock()
		return nil // Already uploaded, idempotent
	}
	s.mu.RUnlock()

	// Update status
	s.mu.Lock()
	upload.Status = UploadStatusUploading
	upload.UpdatedAt = time.Now()
	s.mu.Unlock()

	// Upload chunk to storage backend
	etag, err := s.storage.UploadPart(ctx, upload.StorageKey, upload.BackendUploadID, chunkNumber, reader, size)
	if err != nil {
		return fmt.Errorf("failed to upload chunk: %w", err)
	}

	// Record completed chunk
	s.mu.Lock()
	upload.UploadedChunks[chunkNumber] = storage.CompletedPart{
		PartNumber: chunkNumber,
		ETag:       etag,
		Size:       size,
	}
	upload.UploadedSize += size
	upload.UpdatedAt = time.Now()
	s.mu.Unlock()

	s.logger.Debug("chunk uploaded",
		zap.String("upload_id", uploadID),
		zap.Int("chunk_number", chunkNumber),
		zap.Int64("size", size),
	)

	return nil
}

// CompleteUpload finalizes an upload
func (s *Service) CompleteUpload(ctx context.Context, uploadID string) (*Upload, error) {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	// Verify all chunks are uploaded
	if len(upload.UploadedChunks) != upload.TotalChunks {
		return nil, fmt.Errorf("incomplete upload: %d/%d chunks uploaded", len(upload.UploadedChunks), upload.TotalChunks)
	}

	s.mu.Lock()
	upload.Status = UploadStatusProcessing
	upload.UpdatedAt = time.Now()
	s.mu.Unlock()

	// Sort parts by number
	parts := make([]storage.CompletedPart, 0, len(upload.UploadedChunks))
	for _, part := range upload.UploadedChunks {
		parts = append(parts, part)
	}
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	// Complete multipart upload on storage backend
	if err := s.storage.CompleteMultipartUpload(ctx, upload.StorageKey, upload.BackendUploadID, parts); err != nil {
		s.mu.Lock()
		upload.Status = UploadStatusFailed
		upload.Error = err.Error()
		upload.UpdatedAt = time.Now()
		s.mu.Unlock()
		return nil, fmt.Errorf("failed to complete upload: %w", err)
	}

	// Verify checksum if provided
	if upload.Checksum != "" {
		if err := s.verifyChecksum(ctx, upload); err != nil {
			s.mu.Lock()
			upload.Status = UploadStatusFailed
			upload.Error = err.Error()
			upload.UpdatedAt = time.Now()
			s.mu.Unlock()
			return nil, err
		}
	}

	s.mu.Lock()
	upload.Status = UploadStatusCompleted
	upload.UpdatedAt = time.Now()
	s.mu.Unlock()

	s.logger.Info("upload completed",
		zap.String("upload_id", uploadID),
		zap.String("storage_key", upload.StorageKey),
		zap.Int64("total_size", upload.TotalSize),
	)

	return upload, nil
}

// CancelUpload cancels an in-progress upload
func (s *Service) CancelUpload(ctx context.Context, uploadID string) error {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", uploadID)
	}

	// Abort backend upload
	if err := s.storage.AbortMultipartUpload(ctx, upload.StorageKey, upload.BackendUploadID); err != nil {
		s.logger.Warn("failed to abort backend upload", zap.Error(err))
	}

	s.mu.Lock()
	upload.Status = UploadStatusCancelled
	upload.UpdatedAt = time.Now()
	s.mu.Unlock()

	s.logger.Info("upload cancelled", zap.String("upload_id", uploadID))

	return nil
}

// GetUpload returns upload information
func (s *Service) GetUpload(ctx context.Context, uploadID string) (*Upload, error) {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	return upload, nil
}

// GetProgress returns upload progress information
func (s *Service) GetProgress(ctx context.Context, uploadID string) (*UploadProgress, error) {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	percentComplete := float64(0)
	if upload.TotalSize > 0 {
		percentComplete = float64(upload.UploadedSize) / float64(upload.TotalSize) * 100
	}

	return &UploadProgress{
		UploadID:        uploadID,
		Status:          upload.Status,
		TotalSize:       upload.TotalSize,
		UploadedSize:    upload.UploadedSize,
		TotalChunks:     upload.TotalChunks,
		UploadedChunks:  len(upload.UploadedChunks),
		PercentComplete: percentComplete,
	}, nil
}

// GetMissingChunks returns the list of chunk numbers that haven't been uploaded yet
func (s *Service) GetMissingChunks(ctx context.Context, uploadID string) ([]int, error) {
	s.mu.RLock()
	upload, exists := s.uploads[uploadID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	var missing []int
	for i := 1; i <= upload.TotalChunks; i++ {
		if _, uploaded := upload.UploadedChunks[i]; !uploaded {
			missing = append(missing, i)
		}
	}

	return missing, nil
}

// CleanupExpired removes expired uploads
func (s *Service) CleanupExpired(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for uploadID, upload := range s.uploads {
		if now.After(upload.ExpiresAt) && upload.Status != UploadStatusCompleted {
			// Abort backend upload
			s.storage.AbortMultipartUpload(ctx, upload.StorageKey, upload.BackendUploadID)
			delete(s.uploads, uploadID)

			s.logger.Info("expired upload cleaned up",
				zap.String("upload_id", uploadID),
				zap.Time("expired_at", upload.ExpiresAt),
			)
		}
	}

	return nil
}

// GetUserUploads returns all uploads for a user
func (s *Service) GetUserUploads(ctx context.Context, userID string) ([]*Upload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var uploads []*Upload
	for _, upload := range s.uploads {
		if upload.UserID == userID {
			uploads = append(uploads, upload)
		}
	}

	return uploads, nil
}

// verifyChecksum verifies the SHA256 checksum of the uploaded file
func (s *Service) verifyChecksum(ctx context.Context, upload *Upload) error {
	reader, err := s.storage.Download(ctx, upload.StorageKey)
	if err != nil {
		return fmt.Errorf("failed to download for verification: %w", err)
	}
	defer reader.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	calculated := hex.EncodeToString(hasher.Sum(nil))
	if calculated != upload.Checksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", upload.Checksum, calculated)
	}

	return nil
}

// generateStorageKey creates a storage path for the uploaded file
func generateStorageKey(userID string, fileType FileType, uploadID string, filename string) string {
	typeInfo := fileTypeRegistry[fileType]
	var prefix string
	if typeInfo.IsVM {
		prefix = "vms"
	} else {
		prefix = "docker"
	}
	return fmt.Sprintf("%s/%s/%s/%s", prefix, userID, uploadID, filename)
}

// GetFileTypeInfo returns information about a file type
func GetFileTypeInfo(ft FileType) (FileTypeInfo, bool) {
	info, ok := fileTypeRegistry[ft]
	return info, ok
}

// DetectFileType attempts to detect file type from filename and content type
func DetectFileType(filename, contentType string) FileType {
	// Check by extension first
	for ft, info := range fileTypeRegistry {
		for _, ext := range info.Extensions {
			if ext != "" && len(filename) > len(ext) {
				if filename[len(filename)-len(ext):] == ext {
					return ft
				}
			}
		}
	}

	// Check by content type
	for ft, info := range fileTypeRegistry {
		for _, mime := range info.MimeTypes {
			if mime == contentType {
				return ft
			}
		}
	}

	return FileTypeUnknown
}
