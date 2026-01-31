// Package storage - Google Cloud Storage backend implementation
package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
)

// GCSStorage implements StorageBackend for Google Cloud Storage
type GCSStorage struct {
	client     *storage.Client
	bucketName string
	bucket     *storage.BucketHandle
	logger     *zap.Logger
}

// GCSConfig contains configuration for GCS storage
type GCSConfig struct {
	BucketName      string
	ProjectID       string
	CredentialsFile string // Path to service account JSON (optional if using default credentials)
}

// NewGCSStorage creates a new Google Cloud Storage backend
func NewGCSStorage(ctx context.Context, cfg GCSConfig, logger *zap.Logger) (*GCSStorage, error) {
	var client *storage.Client
	var err error

	// Create client - uses GOOGLE_APPLICATION_CREDENTIALS env var or default credentials
	client, err = storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	bucket := client.Bucket(cfg.BucketName)

	// Verify bucket exists and is accessible
	_, err = bucket.Attrs(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to access bucket %s: %w", cfg.BucketName, err)
	}

	logger.Info("GCS storage initialized",
		zap.String("bucket", cfg.BucketName),
	)

	return &GCSStorage{
		client:     client,
		bucketName: cfg.BucketName,
		bucket:     bucket,
		logger:     logger,
	}, nil
}

// Close closes the GCS client
func (g *GCSStorage) Close() error {
	return g.client.Close()
}

// Upload implements StorageBackend.Upload
func (g *GCSStorage) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	obj := g.bucket.Object(key)
	writer := obj.NewWriter(ctx)

	// Set content type based on extension if possible
	writer.ContentType = "application/octet-stream"

	if size > 0 {
		writer.Size = size
	}

	written, err := io.Copy(writer, reader)
	if err != nil {
		writer.Close()
		return fmt.Errorf("failed to upload to GCS: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize GCS upload: %w", err)
	}

	g.logger.Info("file uploaded to GCS",
		zap.String("key", key),
		zap.Int64("size", written),
	)

	return nil
}

// Download implements StorageBackend.Download
func (g *GCSStorage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	obj := g.bucket.Object(key)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return nil, fmt.Errorf("file not found: %s", key)
		}
		return nil, fmt.Errorf("failed to download from GCS: %w", err)
	}
	return reader, nil
}

// Delete implements StorageBackend.Delete
func (g *GCSStorage) Delete(ctx context.Context, key string) error {
	obj := g.bucket.Object(key)
	if err := obj.Delete(ctx); err != nil {
		if err == storage.ErrObjectNotExist {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete from GCS: %w", err)
	}

	g.logger.Info("file deleted from GCS", zap.String("key", key))
	return nil
}

// Exists implements StorageBackend.Exists
func (g *GCSStorage) Exists(ctx context.Context, key string) (bool, error) {
	obj := g.bucket.Object(key)
	_, err := obj.Attrs(ctx)
	if err == nil {
		return true, nil
	}
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	return false, err
}

// GetURL implements StorageBackend.GetURL
// Returns a signed URL for temporary access
func (g *GCSStorage) GetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	opts := &storage.SignedURLOptions{
		Method:  "GET",
		Expires: time.Now().Add(expiry),
	}

	url, err := g.bucket.SignedURL(key, opts)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}

	return url, nil
}

// GetSize implements StorageBackend.GetSize
func (g *GCSStorage) GetSize(ctx context.Context, key string) (int64, error) {
	obj := g.bucket.Object(key)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get object attributes: %w", err)
	}
	return attrs.Size, nil
}

// InitMultipartUpload implements StorageBackend.InitMultipartUpload
// GCS uses resumable uploads natively, but we'll implement compose-based multipart
func (g *GCSStorage) InitMultipartUpload(ctx context.Context, key string) (string, error) {
	// For GCS, we use object composition approach
	// Upload ID is just a unique prefix for the parts
	uploadID := fmt.Sprintf("multipart/%s/%d", key, time.Now().UnixNano())

	g.logger.Info("GCS multipart upload initiated",
		zap.String("upload_id", uploadID),
		zap.String("key", key),
	)

	return uploadID, nil
}

// UploadPart implements StorageBackend.UploadPart
func (g *GCSStorage) UploadPart(ctx context.Context, key, uploadID string, partNumber int, reader io.Reader, size int64) (string, error) {
	partKey := fmt.Sprintf("%s/part_%d", uploadID, partNumber)

	obj := g.bucket.Object(partKey)
	writer := obj.NewWriter(ctx)

	if size > 0 {
		writer.Size = size
	}

	if _, err := io.Copy(writer, reader); err != nil {
		writer.Close()
		return "", fmt.Errorf("failed to upload part: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize part upload: %w", err)
	}

	// Get the object's CRC32C as ETag equivalent
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get part attributes: %w", err)
	}

	etag := fmt.Sprintf("%d", attrs.CRC32C)

	g.logger.Debug("GCS part uploaded",
		zap.String("upload_id", uploadID),
		zap.Int("part_number", partNumber),
		zap.Int64("size", attrs.Size),
	)

	return etag, nil
}

// CompleteMultipartUpload implements StorageBackend.CompleteMultipartUpload
func (g *GCSStorage) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	// GCS has a limit of 32 objects per compose operation
	// For large files, we need to compose in batches

	const maxComposeObjects = 32

	// Collect all part objects
	var partObjects []*storage.ObjectHandle
	for _, part := range parts {
		partKey := fmt.Sprintf("%s/part_%d", uploadID, part.PartNumber)
		partObjects = append(partObjects, g.bucket.Object(partKey))
	}

	destObj := g.bucket.Object(key)

	if len(partObjects) <= maxComposeObjects {
		// Simple case: compose all at once
		composer := destObj.ComposerFrom(partObjects...)
		if _, err := composer.Run(ctx); err != nil {
			return fmt.Errorf("failed to compose objects: %w", err)
		}
	} else {
		// Complex case: compose in batches
		if err := g.composeInBatches(ctx, destObj, partObjects, maxComposeObjects); err != nil {
			return err
		}
	}

	// Delete all part objects
	for _, partObj := range partObjects {
		if err := partObj.Delete(ctx); err != nil && err != storage.ErrObjectNotExist {
			g.logger.Warn("failed to delete part object", zap.Error(err))
		}
	}

	g.logger.Info("GCS multipart upload completed",
		zap.String("upload_id", uploadID),
		zap.String("key", key),
		zap.Int("total_parts", len(parts)),
	)

	return nil
}

func (g *GCSStorage) composeInBatches(ctx context.Context, destObj *storage.ObjectHandle, parts []*storage.ObjectHandle, batchSize int) error {
	// Compose in batches, creating intermediate objects
	var tempObjects []*storage.ObjectHandle
	batchNum := 0

	for len(parts) > 1 {
		var newParts []*storage.ObjectHandle

		for i := 0; i < len(parts); i += batchSize {
			end := i + batchSize
			if end > len(parts) {
				end = len(parts)
			}

			batch := parts[i:end]

			if len(batch) == 1 {
				newParts = append(newParts, batch[0])
				continue
			}

			// Create intermediate object
			tempKey := fmt.Sprintf("%s_temp_batch_%d", destObj.ObjectName(), batchNum)
			tempObj := g.bucket.Object(tempKey)
			tempObjects = append(tempObjects, tempObj)
			batchNum++

			composer := tempObj.ComposerFrom(batch...)
			if _, err := composer.Run(ctx); err != nil {
				return fmt.Errorf("failed to compose batch: %w", err)
			}

			newParts = append(newParts, tempObj)
		}

		parts = newParts
	}

	// Final compose to destination
	if len(parts) == 1 {
		// Copy the final temp object to destination
		copier := destObj.CopierFrom(parts[0])
		if _, err := copier.Run(ctx); err != nil {
			return fmt.Errorf("failed to copy final object: %w", err)
		}
	}

	// Cleanup temp objects
	for _, tempObj := range tempObjects {
		tempObj.Delete(ctx)
	}

	return nil
}

// AbortMultipartUpload implements StorageBackend.AbortMultipartUpload
func (g *GCSStorage) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	// List and delete all parts
	prefix := uploadID + "/part_"
	it := g.bucket.Objects(ctx, &storage.Query{Prefix: prefix})

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to list parts: %w", err)
		}

		if err := g.bucket.Object(attrs.Name).Delete(ctx); err != nil && err != storage.ErrObjectNotExist {
			g.logger.Warn("failed to delete part", zap.String("part", attrs.Name), zap.Error(err))
		}
	}

	g.logger.Info("GCS multipart upload aborted",
		zap.String("upload_id", uploadID),
	)

	return nil
}

// ListParts implements StorageBackend.ListParts
func (g *GCSStorage) ListParts(ctx context.Context, key, uploadID string) ([]CompletedPart, error) {
	prefix := uploadID + "/part_"
	it := g.bucket.Objects(ctx, &storage.Query{Prefix: prefix})

	var parts []CompletedPart
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list parts: %w", err)
		}

		// Extract part number from name
		var partNum int
		fmt.Sscanf(attrs.Name, uploadID+"/part_%d", &partNum)

		parts = append(parts, CompletedPart{
			PartNumber: partNum,
			ETag:       fmt.Sprintf("%d", attrs.CRC32C),
			Size:       attrs.Size,
		})
	}

	return parts, nil
}
