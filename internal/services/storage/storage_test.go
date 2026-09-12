package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestLocalStorageRejectsEscapingKeys(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "storage")
	store, err := NewLocalStorage(basePath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewLocalStorage() returned an error: %v", err)
	}

	for _, key := range []string{"../outside", "nested/../../outside", `..\outside`, "/tmp/outside"} {
		t.Run(key, func(t *testing.T) {
			err := store.Upload(context.Background(), key, strings.NewReader("probe"), 5)
			if err == nil {
				t.Fatalf("Upload(%q) accepted an escaping key", key)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(basePath), "outside")); !os.IsNotExist(err) {
		t.Fatalf("escaping upload created a file outside storage: %v", err)
	}
}

func TestLocalStorageRejectsSymlinkedParent(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "storage")
	store, err := NewLocalStorage(basePath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewLocalStorage() returned an error: %v", err)
	}

	outsidePath := filepath.Join(tempDir, "outside")
	if err := os.Mkdir(outsidePath, 0755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	symlinkPath := filepath.Join(basePath, "challenge-attachments", "escape")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	err = store.Upload(context.Background(), "challenge-attachments/escape/probe", strings.NewReader("probe"), 5)
	if err == nil {
		t.Fatal("Upload() followed a symlinked parent directory")
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "probe")); !os.IsNotExist(err) {
		t.Fatalf("symlinked upload created a file outside storage: %v", err)
	}
}

func TestLocalStorageRejectsSymlinkedFiles(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "storage")
	store, err := NewLocalStorage(basePath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewLocalStorage() returned an error: %v", err)
	}

	outsidePath := filepath.Join(tempDir, "outside")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0600); err != nil {
		t.Fatalf("create outside file: %v", err)
	}
	symlinkPath := filepath.Join(basePath, "challenge-attachments", "secret")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	key := "challenge-attachments/secret"
	if _, err := store.Download(context.Background(), key); err == nil {
		t.Fatal("Download() followed a symlinked file")
	}
	if _, err := store.Exists(context.Background(), key); err == nil {
		t.Fatal("Exists() followed a symlinked file")
	}
	if _, err := store.GetSize(context.Background(), key); err == nil {
		t.Fatal("GetSize() followed a symlinked file")
	}
	if err := store.Delete(context.Background(), key); err == nil {
		t.Fatal("Delete() accepted a symlinked file")
	}

	contents, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
	if string(contents) != "secret" {
		t.Fatalf("outside file content = %q, want secret", contents)
	}
}

func TestLocalStorageRejectsMismatchedMultipartKey(t *testing.T) {
	store, err := NewLocalStorage(filepath.Join(t.TempDir(), "storage"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewLocalStorage() returned an error: %v", err)
	}

	uploadID, err := store.InitMultipartUpload(context.Background(), "vms/template.qcow2")
	if err != nil {
		t.Fatalf("InitMultipartUpload() returned an error: %v", err)
	}
	if _, err := store.UploadPart(context.Background(), "vms/other.qcow2", uploadID, 1, strings.NewReader("part"), 4); err == nil {
		t.Fatal("UploadPart() accepted a key from another upload")
	}
	if err := store.CompleteMultipartUpload(context.Background(), "vms/other.qcow2", uploadID, nil); err == nil {
		t.Fatal("CompleteMultipartUpload() accepted a key from another upload")
	}
}
