package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/anvil-lab/anvil/internal/services/upload"
)

func TestNormalizeUploadFilename(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: `C:\\fakepath\\image.qcow2`, want: "image.qcow2"},
		{input: "../../Dockerfile", want: "Dockerfile"},
		{input: " normal.tar ", want: "normal.tar"},
		{input: "..", wantErr: true},
		{input: "bad\x00name", wantErr: true},
	}

	for _, tt := range tests {
		got, err := normalizeUploadFilename(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("normalizeUploadFilename(%q) unexpectedly returned %q", tt.input, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("normalizeUploadFilename(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
		}
	}
}

func TestNormalizeUploadChecksum(t *testing.T) {
	upper := strings.Repeat("AB", 32)
	got, err := normalizeUploadChecksum(upper)
	if err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if got != strings.ToLower(upper) {
		t.Fatalf("checksum = %q, want normalized lowercase", got)
	}
	for _, invalid := range []string{"abcd", strings.Repeat("z", 64), strings.Repeat("a", 66)} {
		if _, err := normalizeUploadChecksum(invalid); err == nil {
			t.Errorf("accepted invalid checksum %q", invalid)
		}
	}
}

func TestExpectedUploadChunkSize(t *testing.T) {
	session := &upload.Upload{TotalSize: 25, ChunkSize: 10, TotalChunks: 3}
	for chunk, want := range map[int]int64{1: 10, 2: 10, 3: 5} {
		got, err := expectedUploadChunkSize(session, chunk)
		if err != nil || got != want {
			t.Errorf("chunk %d size = %d, %v; want %d", chunk, got, err, want)
		}
	}
	if _, err := expectedUploadChunkSize(session, 4); err == nil {
		t.Fatal("accepted out-of-range chunk")
	}
	if _, err := expectedUploadChunkSize(&upload.Upload{TotalSize: 30, ChunkSize: 10, TotalChunks: 2}, 2); err == nil {
		t.Fatal("accepted inconsistent final chunk metadata")
	}
}

func TestPublicUploadOmitsStorageSecretsAndErrors(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	session := &upload.Upload{
		ID:              "public-id",
		UserID:          "secret-user-id",
		Filename:        "image.qcow2",
		FileType:        upload.FileTypeQCOW2,
		TotalSize:       10,
		UploadedSize:    10,
		ChunkSize:       10,
		TotalChunks:     1,
		UploadedChunks:  map[int]storage.CompletedPart{1: {PartNumber: 1, ETag: "secret-etag", Size: 10}},
		Status:          upload.UploadStatusCompleted,
		StorageKey:      "secret-storage-key",
		BackendUploadID: "secret-backend-id",
		Checksum:        "secret-checksum",
		Error:           "secret-backend-error",
		CreatedAt:       now,
		UpdatedAt:       now,
		ExpiresAt:       now.Add(time.Hour),
	}

	encoded, err := json.Marshal(publicUpload(session))
	if err != nil {
		t.Fatalf("marshal public upload: %v", err)
	}
	body := string(encoded)
	for _, secret := range []string{"secret-user-id", "secret-etag", "secret-storage-key", "secret-backend-id", "secret-checksum", "secret-backend-error"} {
		if strings.Contains(body, secret) {
			t.Errorf("public response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"uploaded_chunks":1`) {
		t.Fatalf("public response omitted safe progress count: %s", body)
	}
}

func TestSupportedUploadTypeMatchesServerDefaults(t *testing.T) {
	if !supportedUploadType(upload.FileTypeQCOW2) {
		t.Fatal("qcow2 should be supported")
	}
	if supportedUploadType(upload.FileTypeISO) || supportedUploadType(upload.FileTypeVDI) {
		t.Fatal("advertised a type rejected by the server's default upload configuration")
	}
}
