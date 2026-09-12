// Package vmimage provides safe helpers for processing virtual-machine images.
package vmimage

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxExtractedVMDKSize = int64(64 << 30)

// ExtractVMDK extracts the first regular VMDK from an OVA archive into an
// already-created destination directory. Archive paths are validated, but the
// untrusted entry name is never used as the destination path.
func ExtractVMDK(ctx context.Context, ovaPath, destinationDir string) (string, error) {
	destinationInfo, err := os.Lstat(destinationDir)
	if err != nil {
		return "", fmt.Errorf("inspect extraction directory: %w", err)
	}
	if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.IsDir() {
		return "", fmt.Errorf("extraction destination must be a real directory")
	}

	ovaFile, err := os.Open(ovaPath)
	if err != nil {
		return "", fmt.Errorf("open OVA: %w", err)
	}
	defer ovaFile.Close()

	tarReader := tar.NewReader(ovaFile)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("OVA contains no VMDK disk")
		}
		if err != nil {
			return "", fmt.Errorf("read OVA archive: %w", err)
		}
		if !safeArchiveName(header.Name) {
			return "", fmt.Errorf("unsafe OVA entry path %q", header.Name)
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return "", fmt.Errorf("OVA contains unsupported link entry %q", header.Name)
		}
		if !strings.EqualFold(filepath.Ext(header.Name), ".vmdk") {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return "", fmt.Errorf("VMDK entry %q is not a regular file", header.Name)
		}
		if header.Size < 0 || header.Size > maxExtractedVMDKSize {
			return "", fmt.Errorf("VMDK entry size %d is outside the supported range", header.Size)
		}

		destinationPath := filepath.Join(destinationDir, "disk.vmdk")
		destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", fmt.Errorf("create extracted VMDK: %w", err)
		}

		written, copyErr := copyWithContext(ctx, destination, tarReader, header.Size)
		var syncErr error
		if copyErr == nil {
			syncErr = destination.Sync()
		}
		closeErr := destination.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			_ = os.Remove(destinationPath)
			return "", fmt.Errorf("extract VMDK: %w", err)
		}
		if written != header.Size {
			_ = os.Remove(destinationPath)
			return "", fmt.Errorf("extract VMDK: expected %d bytes, wrote %d", header.Size, written)
		}

		return destinationPath, nil
	}
}

func safeArchiveName(name string) bool {
	normalizedName := strings.ReplaceAll(name, "\\", "/")
	cleanName := filepath.Clean(filepath.FromSlash(normalizedName))
	if cleanName == "." {
		return normalizedName == "." || normalizedName == "./"
	}

	return !filepath.IsAbs(cleanName) &&
		cleanName != ".." &&
		!strings.HasPrefix(cleanName, ".."+string(os.PathSeparator))
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, size int64) (int64, error) {
	limitedSource := io.LimitReader(source, size)
	buffer := make([]byte, 1024*1024)
	var written int64

	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := limitedSource.Read(buffer)
		if read > 0 {
			count, writeErr := destination.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
