// Package storage provides abstracted file storage supporting local filesystem
// and cloud storage backends (GCS, S3). Designed for large file uploads like
// OVA images (2-10GB+) with chunked upload support.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// StorageBackend defines the interface for storage providers
type StorageBackend interface {
	// Upload uploads a file to the storage backend
	Upload(ctx context.Context, key string, reader io.Reader, size int64) error

	// Download retrieves a file from storage
	Download(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes a file from storage
	Delete(ctx context.Context, key string) error

	// Exists checks if a file exists
	Exists(ctx context.Context, key string) (bool, error)

	// GetURL returns a URL for accessing the file (may be signed/temporary)
	GetURL(ctx context.Context, key string, expiry time.Duration) (string, error)

	// GetSize returns the size of a stored file
	GetSize(ctx context.Context, key string) (int64, error)

	// InitMultipartUpload starts a chunked upload session
	InitMultipartUpload(ctx context.Context, key string) (uploadID string, err error)

	// UploadPart uploads a single chunk
	UploadPart(ctx context.Context, key, uploadID string, partNumber int, reader io.Reader, size int64) (etag string, err error)

	// CompleteMultipartUpload finalizes a chunked upload
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error

	// AbortMultipartUpload cancels an in-progress chunked upload
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error

	// ListParts returns uploaded parts for a multipart upload
	ListParts(ctx context.Context, key, uploadID string) ([]CompletedPart, error)
}

// CompletedPart represents a successfully uploaded chunk
type CompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

// UploadMetadata contains information about an upload
type UploadMetadata struct {
	ID           string          `json:"id"`
	Key          string          `json:"key"`
	Filename     string          `json:"filename"`
	ContentType  string          `json:"content_type"`
	TotalSize    int64           `json:"total_size"`
	ChunkSize    int64           `json:"chunk_size"`
	TotalChunks  int             `json:"total_chunks"`
	UploadedParts []CompletedPart `json:"uploaded_parts"`
	Status       string          `json:"status"` // pending, uploading, processing, completed, failed
	Checksum     string          `json:"checksum"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

// LocalStorage implements StorageBackend for local filesystem
type LocalStorage struct {
	basePath     string
	tempPath     string
	logger       *zap.Logger
	mu           sync.RWMutex
	activeUploads map[string]*localUploadState
}

type localUploadState struct {
	key         string
	tempDir     string
	parts       map[int]*CompletedPart
	mu          sync.Mutex
	createdAt   time.Time
}

// NewLocalStorage creates a new local filesystem storage backend
func NewLocalStorage(basePath string, logger *zap.Logger) (*LocalStorage, error) {
	// Ensure base directories exist
	dirs := []string{
		basePath,
		filepath.Join(basePath, "challenges"),
		filepath.Join(basePath, "vms"),
		filepath.Join(basePath, "docker"),
		filepath.Join(basePath, "temp"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return &LocalStorage{
		basePath:      basePath,
		tempPath:      filepath.Join(basePath, "temp"),
		logger:        logger,
		activeUploads: make(map[string]*localUploadState),
	}, nil
}

func (l *LocalStorage) fullPath(key string) string {
	return filepath.Join(l.basePath, key)
}

// Upload implements StorageBackend.Upload
func (l *LocalStorage) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	fullPath := l.fullPath(key)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create file
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy with context cancellation support
	written, err := copyWithContext(ctx, file, reader)
	if err != nil {
		os.Remove(fullPath) // Cleanup on failure
		return fmt.Errorf("failed to write file: %w", err)
	}

	if size > 0 && written != size {
		os.Remove(fullPath)
		return fmt.Errorf("size mismatch: expected %d, got %d", size, written)
	}

	l.logger.Info("file uploaded",
		zap.String("key", key),
		zap.Int64("size", written),
	)

	return nil
}

// Download implements StorageBackend.Download
func (l *LocalStorage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	fullPath := l.fullPath(key)

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", key)
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return file, nil
}

// Delete implements StorageBackend.Delete
func (l *LocalStorage) Delete(ctx context.Context, key string) error {
	fullPath := l.fullPath(key)

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}

	l.logger.Info("file deleted", zap.String("key", key))
	return nil
}

// Exists implements StorageBackend.Exists
func (l *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	fullPath := l.fullPath(key)
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// GetURL implements StorageBackend.GetURL
// For local storage, returns a file:// URL (for dev) or path to be served via HTTP
func (l *LocalStorage) GetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	fullPath := l.fullPath(key)
	exists, err := l.Exists(ctx, key)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("file not found: %s", key)
	}
	// Return relative path - actual URL construction handled by API layer
	return fullPath, nil
}

// GetSize implements StorageBackend.GetSize
func (l *LocalStorage) GetSize(ctx context.Context, key string) (int64, error) {
	fullPath := l.fullPath(key)
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat file: %w", err)
	}
	return info.Size(), nil
}

// InitMultipartUpload implements StorageBackend.InitMultipartUpload
func (l *LocalStorage) InitMultipartUpload(ctx context.Context, key string) (string, error) {
	uploadID := generateUploadID()

	// Create temp directory for this upload
	tempDir := filepath.Join(l.tempPath, uploadID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	l.mu.Lock()
	l.activeUploads[uploadID] = &localUploadState{
		key:       key,
		tempDir:   tempDir,
		parts:     make(map[int]*CompletedPart),
		createdAt: time.Now(),
	}
	l.mu.Unlock()

	l.logger.Info("multipart upload initiated",
		zap.String("upload_id", uploadID),
		zap.String("key", key),
	)

	return uploadID, nil
}

// UploadPart implements StorageBackend.UploadPart
func (l *LocalStorage) UploadPart(ctx context.Context, key, uploadID string, partNumber int, reader io.Reader, size int64) (string, error) {
	l.mu.RLock()
	state, exists := l.activeUploads[uploadID]
	l.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("upload not found: %s", uploadID)
	}

	// Create part file
	partPath := filepath.Join(state.tempDir, fmt.Sprintf("part_%d", partNumber))
	file, err := os.Create(partPath)
	if err != nil {
		return "", fmt.Errorf("failed to create part file: %w", err)
	}
	defer file.Close()

	// Write part and calculate checksum
	hasher := sha256.New()
	writer := io.MultiWriter(file, hasher)

	written, err := copyWithContext(ctx, writer, reader)
	if err != nil {
		os.Remove(partPath)
		return "", fmt.Errorf("failed to write part: %w", err)
	}

	if size > 0 && written != size {
		os.Remove(partPath)
		return "", fmt.Errorf("part size mismatch: expected %d, got %d", size, written)
	}

	etag := hex.EncodeToString(hasher.Sum(nil))

	// Record completed part
	state.mu.Lock()
	state.parts[partNumber] = &CompletedPart{
		PartNumber: partNumber,
		ETag:       etag,
		Size:       written,
	}
	state.mu.Unlock()

	l.logger.Debug("part uploaded",
		zap.String("upload_id", uploadID),
		zap.Int("part_number", partNumber),
		zap.Int64("size", written),
	)

	return etag, nil
}

// CompleteMultipartUpload implements StorageBackend.CompleteMultipartUpload
func (l *LocalStorage) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	l.mu.RLock()
	state, exists := l.activeUploads[uploadID]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", uploadID)
	}

	fullPath := l.fullPath(key)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create final file
	finalFile, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	defer finalFile.Close()

	// Concatenate parts in order
	for _, part := range parts {
		partPath := filepath.Join(state.tempDir, fmt.Sprintf("part_%d", part.PartNumber))
		partFile, err := os.Open(partPath)
		if err != nil {
			finalFile.Close()
			os.Remove(fullPath)
			return fmt.Errorf("failed to open part %d: %w", part.PartNumber, err)
		}

		_, err = io.Copy(finalFile, partFile)
		partFile.Close()
		if err != nil {
			finalFile.Close()
			os.Remove(fullPath)
			return fmt.Errorf("failed to copy part %d: %w", part.PartNumber, err)
		}
	}

	// Cleanup temp files
	os.RemoveAll(state.tempDir)

	l.mu.Lock()
	delete(l.activeUploads, uploadID)
	l.mu.Unlock()

	l.logger.Info("multipart upload completed",
		zap.String("upload_id", uploadID),
		zap.String("key", key),
		zap.Int("total_parts", len(parts)),
	)

	return nil
}

// AbortMultipartUpload implements StorageBackend.AbortMultipartUpload
func (l *LocalStorage) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	l.mu.Lock()
	state, exists := l.activeUploads[uploadID]
	if exists {
		delete(l.activeUploads, uploadID)
	}
	l.mu.Unlock()

	if !exists {
		return nil // Already cleaned up
	}

	// Remove temp directory
	os.RemoveAll(state.tempDir)

	l.logger.Info("multipart upload aborted",
		zap.String("upload_id", uploadID),
		zap.String("key", key),
	)

	return nil
}

// ListParts implements StorageBackend.ListParts
func (l *LocalStorage) ListParts(ctx context.Context, key, uploadID string) ([]CompletedPart, error) {
	l.mu.RLock()
	state, exists := l.activeUploads[uploadID]
	l.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	parts := make([]CompletedPart, 0, len(state.parts))
	for _, part := range state.parts {
		parts = append(parts, *part)
	}

	return parts, nil
}

// CleanupStaleUploads removes uploads older than the specified duration
func (l *LocalStorage) CleanupStaleUploads(maxAge time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for uploadID, state := range l.activeUploads {
		if now.Sub(state.createdAt) > maxAge {
			os.RemoveAll(state.tempDir)
			delete(l.activeUploads, uploadID)
			l.logger.Info("cleaned up stale upload",
				zap.String("upload_id", uploadID),
				zap.Duration("age", now.Sub(state.createdAt)),
			)
		}
	}

	return nil
}

// Helper functions

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024) // 32KB buffer
	var written int64

	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		nr, readErr := src.Read(buf)
		if nr > 0 {
			nw, writeErr := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if writeErr != nil {
				return written, writeErr
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func generateUploadID() string {
	b := make([]byte, 16)
	// Use crypto/rand in production
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8))
	}
	return hex.EncodeToString(b)
}
