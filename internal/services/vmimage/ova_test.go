package vmimage

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"
)

type archiveEntry struct {
	name     string
	typeflag byte
	linkname string
	contents string
}

func writeTestOVA(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create OVA: %v", err)
	}
	writer := tar.NewWriter(file)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0600,
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
			Size:     int64(len(entry.contents)),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if entry.contents != "" {
			if _, err := writer.Write([]byte(entry.contents)); err != nil {
				t.Fatalf("write tar content: %v", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close OVA: %v", err)
	}
}

func TestExtractVMDKUsesManagedFilename(t *testing.T) {
	tempDir := t.TempDir()
	ovaPath := filepath.Join(tempDir, "image.ova")
	extractDir := filepath.Join(tempDir, "extract")
	if err := os.Mkdir(extractDir, 0700); err != nil {
		t.Fatalf("create extraction directory: %v", err)
	}
	writeTestOVA(t, ovaPath, []archiveEntry{{
		name:     "nested/original-name.vmdk",
		typeflag: tar.TypeReg,
		contents: "virtual disk",
	}})

	gotPath, err := ExtractVMDK(context.Background(), ovaPath, extractDir)
	if err != nil {
		t.Fatalf("ExtractVMDK() returned an error: %v", err)
	}
	wantPath := filepath.Join(extractDir, "disk.vmdk")
	if gotPath != wantPath {
		t.Fatalf("ExtractVMDK() path = %q, want %q", gotPath, wantPath)
	}
	contents, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("read extracted VMDK: %v", err)
	}
	if string(contents) != "virtual disk" {
		t.Fatalf("extracted contents = %q", contents)
	}
}

func TestExtractVMDKRejectsTraversal(t *testing.T) {
	tempDir := t.TempDir()
	ovaPath := filepath.Join(tempDir, "image.ova")
	extractDir := filepath.Join(tempDir, "extract")
	if err := os.Mkdir(extractDir, 0700); err != nil {
		t.Fatalf("create extraction directory: %v", err)
	}
	writeTestOVA(t, ovaPath, []archiveEntry{{
		name:     "../../escape.vmdk",
		typeflag: tar.TypeReg,
		contents: "escape",
	}})

	if _, err := ExtractVMDK(context.Background(), ovaPath, extractDir); err == nil {
		t.Fatal("ExtractVMDK() accepted a traversal entry")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "escape.vmdk")); !os.IsNotExist(err) {
		t.Fatalf("traversal entry created an outside file: %v", err)
	}
}

func TestExtractVMDKRejectsLinks(t *testing.T) {
	tempDir := t.TempDir()
	ovaPath := filepath.Join(tempDir, "image.ova")
	extractDir := filepath.Join(tempDir, "extract")
	if err := os.Mkdir(extractDir, 0700); err != nil {
		t.Fatalf("create extraction directory: %v", err)
	}
	writeTestOVA(t, ovaPath, []archiveEntry{{
		name:     "disk.vmdk",
		typeflag: tar.TypeSymlink,
		linkname: "/etc/passwd",
	}})

	if _, err := ExtractVMDK(context.Background(), ovaPath, extractDir); err == nil {
		t.Fatal("ExtractVMDK() accepted a symlink entry")
	}
}
