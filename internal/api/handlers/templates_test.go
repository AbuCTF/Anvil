package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileDurablyCopiesContents(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.qcow2")
	destinationPath := filepath.Join(tempDir, "destination.qcow2")
	want := []byte("qcow2 test content")

	if err := os.WriteFile(sourcePath, want, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyFileDurably(sourcePath, destinationPath); err != nil {
		t.Fatalf("copyFileDurably() error = %v", err)
	}

	got, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("destination content = %q, want %q", got, want)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source should remain until its caller removes it: %v", err)
	}
}

func TestCopyFileDurablyMissingSourceDoesNotCreateDestination(t *testing.T) {
	tempDir := t.TempDir()
	destinationPath := filepath.Join(tempDir, "destination.qcow2")

	if err := copyFileDurably(filepath.Join(tempDir, "missing.qcow2"), destinationPath); err == nil {
		t.Fatal("copyFileDurably() error = nil, want source-open error")
	}
	if _, err := os.Stat(destinationPath); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist, stat error = %v", err)
	}
}
