package models

import (
	"encoding/json"
	"time"
)

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

type Upload struct {
	ID          string  `json:"id" db:"id"`
	UserID      string  `json:"user_id" db:"user_id"`
	ChallengeID *string `json:"challenge_id,omitempty" db:"challenge_id"`

	Filename     string  `json:"filename" db:"filename"`
	FileType     string  `json:"file_type" db:"file_type"` // dockerfile, docker_context, docker_image, ova, vmdk, qcow2, etc.
	ContentType  *string `json:"content_type,omitempty" db:"content_type"`
	TotalSize    int64   `json:"total_size" db:"total_size"`
	UploadedSize int64   `json:"uploaded_size" db:"uploaded_size"`

	ChunkSize      int `json:"chunk_size" db:"chunk_size"`
	TotalChunks    int `json:"total_chunks" db:"total_chunks"`
	UploadedChunks int `json:"uploaded_chunks" db:"uploaded_chunks"`

	StorageKey      string  `json:"storage_key" db:"storage_key"`
	StorageBackend  string  `json:"storage_backend" db:"storage_backend"` // local, gcs, s3
	BackendUploadID *string `json:"backend_upload_id,omitempty" db:"backend_upload_id"`

	Status       UploadStatus `json:"status" db:"status"`
	ErrorMessage *string      `json:"error_message,omitempty" db:"error_message"`

	ChecksumExpected *string `json:"checksum_expected,omitempty" db:"checksum_expected"`
	ChecksumActual   *string `json:"checksum_actual,omitempty" db:"checksum_actual"`

	ProcessedPath *string `json:"processed_path,omitempty" db:"processed_path"`
	ProcessingLog *string `json:"processing_log,omitempty" db:"processing_log"`

	ValidationPassed  *bool           `json:"validation_passed,omitempty" db:"validation_passed"`
	ValidationResults json.RawMessage `json:"validation_results,omitempty" db:"validation_results"`

	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" db:"expires_at"`
}

type UploadChunk struct {
	ID          string    `json:"id" db:"id"`
	UploadID    string    `json:"upload_id" db:"upload_id"`
	ChunkNumber int       `json:"chunk_number" db:"chunk_number"`
	Size        int64     `json:"size" db:"size"`
	ETag        *string   `json:"etag,omitempty" db:"etag"`
	UploadedAt  time.Time `json:"uploaded_at" db:"uploaded_at"`
}

func (u *Upload) GetValidationResults() (map[string]interface{}, error) {
	if u.ValidationResults == nil {
		return nil, nil
	}
	var results map[string]interface{}
	if err := json.Unmarshal(u.ValidationResults, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (u *Upload) Progress() float64 {
	if u.TotalSize == 0 {
		return 0
	}
	return float64(u.UploadedSize) / float64(u.TotalSize) * 100
}

func (u *Upload) IsComplete() bool {
	return u.UploadedChunks >= u.TotalChunks
}

type CreateUploadRequest struct {
	Filename    string  `json:"filename" binding:"required"`
	FileType    string  `json:"file_type" binding:"required"`
	TotalSize   int64   `json:"total_size" binding:"required,gt=0"`
	ContentType string  `json:"content_type,omitempty"`
	ChunkSize   int     `json:"chunk_size,omitempty"`
	Checksum    string  `json:"checksum,omitempty"` // sha256 of complete file
	ChallengeID *string `json:"challenge_id,omitempty"`
}

type UploadResponse struct {
	ID             string       `json:"id"`
	Status         UploadStatus `json:"status"`
	ChunkSize      int          `json:"chunk_size"`
	TotalChunks    int          `json:"total_chunks"`
	UploadedChunks int          `json:"uploaded_chunks"`
	Progress       float64      `json:"progress"`
	ExpiresAt      *time.Time   `json:"expires_at,omitempty"`
	StorageKey     string       `json:"storage_key,omitempty"`
}

type UploadProgressResponse struct {
	ID                     string       `json:"id"`
	Status                 UploadStatus `json:"status"`
	TotalSize              int64        `json:"total_size"`
	UploadedSize           int64        `json:"uploaded_size"`
	TotalChunks            int          `json:"total_chunks"`
	UploadedChunks         int          `json:"uploaded_chunks"`
	MissingChunks          []int        `json:"missing_chunks,omitempty"`
	Progress               float64      `json:"progress"`
	BytesPerSecond         int64        `json:"bytes_per_second,omitempty"`
	EstimatedTimeRemaining int64        `json:"estimated_time_remaining,omitempty"`
}
