package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestCopyFileDurablyDoesNotOverwriteDestination(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.qcow2")
	destinationPath := filepath.Join(tempDir, "destination.qcow2")
	if err := os.WriteFile(sourcePath, []byte("new image"), 0600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte("existing image"), 0600); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	if err := copyFileDurably(sourcePath, destinationPath); err == nil {
		t.Fatal("copyFileDurably() overwrote an existing destination")
	}
	contents, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(contents) != "existing image" {
		t.Fatalf("destination contents = %q, want existing image", contents)
	}
}

func TestRemoveFileWithinRemovesManagedRegularFile(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "templates")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	targetPath := filepath.Join(rootPath, "template.qcow2")
	if err := os.WriteFile(targetPath, []byte("image"), 0600); err != nil {
		t.Fatalf("create target: %v", err)
	}

	if err := removeFileWithin(rootPath, targetPath); err != nil {
		t.Fatalf("removeFileWithin() returned an error: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("managed file still exists: %v", err)
	}
}

func TestRemoveFileWithinRejectsOutsideAndSymlinkedPaths(t *testing.T) {
	tempDir := t.TempDir()
	rootPath := filepath.Join(tempDir, "templates")
	outsideDir := filepath.Join(tempDir, "outside")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := os.Mkdir(outsideDir, 0755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	outsidePath := filepath.Join(outsideDir, "keep.qcow2")
	if err := os.WriteFile(outsidePath, []byte("keep"), 0600); err != nil {
		t.Fatalf("create outside target: %v", err)
	}

	if err := removeFileWithin(rootPath, outsidePath); err == nil {
		t.Fatal("removeFileWithin() accepted a file outside the managed root")
	}
	symlinkedParent := filepath.Join(rootPath, "linked")
	if err := os.Symlink(outsideDir, symlinkedParent); err != nil {
		t.Fatalf("create parent symlink: %v", err)
	}
	if err := removeFileWithin(rootPath, filepath.Join(symlinkedParent, "keep.qcow2")); err == nil {
		t.Fatal("removeFileWithin() accepted a symlinked parent")
	}
	symlinkedFile := filepath.Join(rootPath, "linked.qcow2")
	if err := os.Symlink(outsidePath, symlinkedFile); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}
	if err := removeFileWithin(rootPath, symlinkedFile); err == nil {
		t.Fatal("removeFileWithin() accepted a symlinked file")
	}

	if contents, err := os.ReadFile(outsidePath); err != nil || string(contents) != "keep" {
		t.Fatalf("outside file was changed: contents=%q err=%v", contents, err)
	}
}

func TestParsePositiveTemplateResource(t *testing.T) {
	tests := []struct {
		value   string
		want    int
		wantErr bool
	}{
		{value: " 4 ", want: 4},
		{value: "0", wantErr: true},
		{value: "-1", wantErr: true},
		{value: "1.5", wantErr: true},
		{value: "not-a-number", wantErr: true},
		{value: "2147483648", wantErr: true},
	}

	for _, test := range tests {
		t.Run(strings.TrimSpace(test.value), func(t *testing.T) {
			got, err := parsePositiveTemplateResource(test.value, "resource")
			if test.wantErr {
				if err == nil {
					t.Fatalf("parsePositiveTemplateResource(%q) error = nil", test.value)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("parsePositiveTemplateResource(%q) = %d, %v; want %d, nil", test.value, got, err, test.want)
			}
		})
	}
}

func TestNilVMTemplateHandlerReturnsServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/vm-templates", nil)

	var handler *VMTemplateHandler
	handler.List(ctx)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestCappedCommandOutputDiscardsExcessWithoutShortWrite(t *testing.T) {
	output := &cappedCommandOutput{limit: 16}
	input := bytes.Repeat([]byte("x"), 32)
	written, err := output.Write(input)
	if err != nil || written != len(input) {
		t.Fatalf("Write() = %d, %v; want %d, nil", written, err, len(input))
	}
	if output.buffer.Len() != 16 {
		t.Fatalf("buffer length = %d, want 16", output.buffer.Len())
	}
}
