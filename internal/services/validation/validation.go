// Package validation provides security validation for uploaded files
// including format verification, malware scanning, and content inspection.
package validation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// ValidationResult contains the results of file validation
type ValidationResult struct {
	Valid           bool              `json:"valid"`
	FileType        string            `json:"file_type"`
	DetectedFormat  string            `json:"detected_format"`
	Size            int64             `json:"size"`
	Checksum        string            `json:"checksum"`
	Errors          []string          `json:"errors,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	MalwareScanResult *MalwareScanResult `json:"malware_scan,omitempty"`
}

// MalwareScanResult contains malware scan results
type MalwareScanResult struct {
	Scanned     bool     `json:"scanned"`
	Clean       bool     `json:"clean"`
	Threats     []string `json:"threats,omitempty"`
	ScannerUsed string   `json:"scanner_used,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// Validator handles file validation
type Validator struct {
	logger         *zap.Logger
	config         Config
	clamavEnabled  bool
}

// Config contains validation configuration
type Config struct {
	MaxFileSizeDocker int64    // Max size for Docker-related files
	MaxFileSizeVM     int64    // Max size for VM images
	AllowedExtensions []string // Allowed file extensions
	EnableMalwareScan bool     // Whether to run malware scans
	ClamAVSocket      string   // Path to ClamAV socket
	QuarantinePath    string   // Where to quarantine suspicious files
	TempPath          string   // Temp directory for validation
}

// DefaultConfig returns default validation configuration
func DefaultConfig() Config {
	return Config{
		MaxFileSizeDocker: 10 * 1024 * 1024 * 1024,  // 10GB
		MaxFileSizeVM:     50 * 1024 * 1024 * 1024,  // 50GB
		AllowedExtensions: []string{
			".tar", ".tar.gz", ".tgz", ".gz",
			".ova", ".ovf", ".vmdk", ".vdi", ".qcow2", ".qcow",
			".iso", ".img",
			"Dockerfile", "dockerfile",
		},
		EnableMalwareScan: true,
		ClamAVSocket:      "/var/run/clamav/clamd.ctl",
		QuarantinePath:    "/var/lib/anvil/quarantine",
		TempPath:          "/tmp/anvil-validation",
	}
}

// NewValidator creates a new file validator
func NewValidator(logger *zap.Logger, config Config) (*Validator, error) {
	// Create directories
	for _, dir := range []string{config.QuarantinePath, config.TempPath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	v := &Validator{
		logger: logger,
		config: config,
	}

	// Check if ClamAV is available
	if config.EnableMalwareScan {
		v.clamavEnabled = v.checkClamAV()
		if !v.clamavEnabled {
			logger.Warn("ClamAV not available, malware scanning disabled")
		}
	}

	return v, nil
}

// ValidateFile performs comprehensive validation on a file
func (v *Validator) ValidateFile(ctx context.Context, filePath string, expectedType string) (*ValidationResult, error) {
	result := &ValidationResult{
		Valid:    true,
		Metadata: make(map[string]string),
	}

	// Check file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	result.Size = fileInfo.Size()

	// Calculate checksum
	checksum, err := v.calculateChecksum(filePath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("checksum calculation failed: %v", err))
		result.Valid = false
	}
	result.Checksum = checksum

	// Detect file type using magic bytes
	detectedType, err := v.detectFileType(filePath)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("file type detection failed: %v", err))
	}
	result.DetectedFormat = detectedType

	// Validate based on expected type
	switch expectedType {
	case "dockerfile":
		if err := v.validateDockerfile(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "docker_context":
		if err := v.validateDockerContext(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "docker_image":
		if err := v.validateDockerImage(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "ova":
		if err := v.validateOVA(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "vmdk":
		if err := v.validateVMDK(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "qcow2":
		if err := v.validateQCOW2(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	case "iso":
		if err := v.validateISO(filePath, result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Valid = false
		}
	default:
		result.Warnings = append(result.Warnings, fmt.Sprintf("unknown file type: %s", expectedType))
	}

	// Run malware scan if enabled
	if v.config.EnableMalwareScan {
		malwareResult := v.scanForMalware(ctx, filePath)
		result.MalwareScanResult = malwareResult
		if malwareResult.Scanned && !malwareResult.Clean {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("malware detected: %v", malwareResult.Threats))

			// Quarantine the file
			if err := v.quarantineFile(filePath); err != nil {
				v.logger.Error("failed to quarantine file", zap.String("path", filePath), zap.Error(err))
			}
		}
	}

	result.FileType = expectedType
	return result, nil
}

// validateDockerfile checks if a file is a valid Dockerfile
func (v *Validator) validateDockerfile(filePath string, result *ValidationResult) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Check for FROM instruction (required)
	hasFrom := regexp.MustCompile(`(?im)^FROM\s+`).Match(content)
	if !hasFrom {
		return errors.New("Dockerfile must contain a FROM instruction")
	}

	// Check for potentially dangerous instructions
	dangerousPatterns := []struct {
		pattern *regexp.Regexp
		message string
	}{
		{regexp.MustCompile(`(?im)^ADD\s+https?://`), "Using ADD with remote URLs is discouraged, use COPY + curl instead"},
		{regexp.MustCompile(`(?im)curl\s+.*\|\s*sh`), "Piping curl to shell is dangerous"},
		{regexp.MustCompile(`(?im)wget\s+.*\|\s*sh`), "Piping wget to shell is dangerous"},
	}

	for _, dp := range dangerousPatterns {
		if dp.pattern.Match(content) {
			result.Warnings = append(result.Warnings, dp.message)
		}
	}

	// Extract base image
	fromMatch := regexp.MustCompile(`(?im)^FROM\s+(\S+)`).FindSubmatch(content)
	if len(fromMatch) > 1 {
		result.Metadata["base_image"] = string(fromMatch[1])
	}

	// Count instructions
	instructionCount := len(regexp.MustCompile(`(?im)^(FROM|RUN|COPY|ADD|ENV|EXPOSE|CMD|ENTRYPOINT|WORKDIR|USER|VOLUME|ARG|LABEL|HEALTHCHECK|SHELL)\s+`).FindAllIndex(content, -1))
	result.Metadata["instruction_count"] = fmt.Sprintf("%d", instructionCount)

	return nil
}

// validateDockerContext validates a Docker build context archive
func (v *Validator) validateDockerContext(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Detect if gzipped
	var tarReader *tar.Reader
	header := make([]byte, 2)
	file.Read(header)
	file.Seek(0, 0)

	if header[0] == 0x1f && header[1] == 0x8b {
		// Gzipped
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("invalid gzip archive: %w", err)
		}
		defer gzReader.Close()
		tarReader = tar.NewReader(gzReader)
	} else {
		tarReader = tar.NewReader(file)
	}

	hasDockerfile := false
	fileCount := 0
	var totalSize int64

	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid tar archive: %w", err)
		}

		fileCount++
		totalSize += hdr.Size

		// Check for Dockerfile
		if hdr.Name == "Dockerfile" || strings.HasSuffix(hdr.Name, "/Dockerfile") {
			hasDockerfile = true
		}

		// Check for path traversal attacks
		if strings.Contains(hdr.Name, "..") {
			return errors.New("archive contains path traversal (..)")
		}

		// Check for suspicious files
		if strings.HasPrefix(hdr.Name, "/") {
			result.Warnings = append(result.Warnings, fmt.Sprintf("absolute path in archive: %s", hdr.Name))
		}
	}

	if !hasDockerfile {
		return errors.New("no Dockerfile found in archive")
	}

	result.Metadata["file_count"] = fmt.Sprintf("%d", fileCount)
	result.Metadata["uncompressed_size"] = fmt.Sprintf("%d", totalSize)

	return nil
}

// validateDockerImage validates an exported Docker image tar
func (v *Validator) validateDockerImage(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	tarReader := tar.NewReader(file)

	hasManifest := false
	layerCount := 0

	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid tar archive: %w", err)
		}

		if hdr.Name == "manifest.json" {
			hasManifest = true
		}

		if strings.HasSuffix(hdr.Name, "/layer.tar") {
			layerCount++
		}
	}

	if !hasManifest {
		return errors.New("not a valid Docker image: missing manifest.json")
	}

	result.Metadata["layer_count"] = fmt.Sprintf("%d", layerCount)

	return nil
}

// validateOVA validates an OVA file
func (v *Validator) validateOVA(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	tarReader := tar.NewReader(file)

	hasOVF := false
	hasVMDK := false
	var ovfName string

	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid OVA (tar) archive: %w", err)
		}

		if strings.HasSuffix(strings.ToLower(hdr.Name), ".ovf") {
			hasOVF = true
			ovfName = hdr.Name
		}
		if strings.HasSuffix(strings.ToLower(hdr.Name), ".vmdk") {
			hasVMDK = true
		}
	}

	if !hasOVF {
		return errors.New("OVA missing .ovf descriptor file")
	}

	if !hasVMDK {
		result.Warnings = append(result.Warnings, "OVA does not contain VMDK disk image")
	}

	result.Metadata["ovf_file"] = ovfName

	return nil
}

// validateVMDK validates a VMDK file
func (v *Validator) validateVMDK(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read VMDK header
	header := make([]byte, 512)
	if _, err := file.Read(header); err != nil {
		return err
	}

	// Check for sparse VMDK signature "KDMV"
	if bytes.HasPrefix(header, []byte("KDMV")) {
		result.Metadata["vmdk_type"] = "sparse"
		return nil
	}

	// Check for descriptor file (text-based)
	if bytes.Contains(header[:100], []byte("# Disk DescriptorFile")) {
		result.Metadata["vmdk_type"] = "descriptor"
		return nil
	}

	// Could be a raw or flat VMDK
	result.Metadata["vmdk_type"] = "unknown"
	result.Warnings = append(result.Warnings, "Could not determine VMDK type, may be flat/raw format")

	return nil
}

// validateQCOW2 validates a QCOW2 file
func (v *Validator) validateQCOW2(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read QCOW2 header
	header := make([]byte, 104)
	if _, err := file.Read(header); err != nil {
		return err
	}

	// Check magic number "QFI\xfb"
	if !bytes.HasPrefix(header, []byte{0x51, 0x46, 0x49, 0xfb}) {
		return errors.New("not a valid QCOW2 file: invalid magic number")
	}

	// Get version (offset 4, 4 bytes big-endian)
	version := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])
	result.Metadata["qcow2_version"] = fmt.Sprintf("%d", version)

	if version != 2 && version != 3 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("unusual QCOW2 version: %d", version))
	}

	// Get virtual size (offset 24, 8 bytes big-endian)
	virtualSize := uint64(header[24])<<56 | uint64(header[25])<<48 | uint64(header[26])<<40 | uint64(header[27])<<32 |
		uint64(header[28])<<24 | uint64(header[29])<<16 | uint64(header[30])<<8 | uint64(header[31])
	result.Metadata["virtual_size"] = fmt.Sprintf("%d", virtualSize)

	return nil
}

// validateISO validates an ISO file
func (v *Validator) validateISO(filePath string, result *ValidationResult) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// ISO 9660 primary volume descriptor starts at sector 16 (32768 bytes)
	// with magic string "CD001"
	file.Seek(32769, 0)
	magic := make([]byte, 5)
	if _, err := file.Read(magic); err != nil {
		return fmt.Errorf("failed to read ISO header: %w", err)
	}

	if !bytes.Equal(magic, []byte("CD001")) {
		return errors.New("not a valid ISO 9660 file")
	}

	// Read volume label (offset 32808, 32 bytes)
	file.Seek(32808, 0)
	label := make([]byte, 32)
	file.Read(label)
	result.Metadata["volume_label"] = strings.TrimSpace(string(label))

	return nil
}

// calculateChecksum calculates SHA256 checksum of a file
func (v *Validator) calculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// detectFileType detects file type using magic bytes
func (v *Validator) detectFileType(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	header := make([]byte, 512)
	n, _ := file.Read(header)
	header = header[:n]

	// Check various magic signatures
	switch {
	case bytes.HasPrefix(header, []byte{0x1f, 0x8b}):
		return "gzip", nil
	case bytes.HasPrefix(header, []byte{0x50, 0x4b, 0x03, 0x04}):
		return "zip", nil
	case bytes.HasPrefix(header, []byte("ustar")):
		return "tar", nil
	case n >= 257 && bytes.Equal(header[257:262], []byte("ustar")):
		return "tar", nil
	case bytes.HasPrefix(header, []byte{0x51, 0x46, 0x49, 0xfb}):
		return "qcow2", nil
	case bytes.HasPrefix(header, []byte("KDMV")):
		return "vmdk", nil
	case bytes.Contains(header[:100], []byte("# Disk DescriptorFile")):
		return "vmdk-descriptor", nil
	case bytes.HasPrefix(header, []byte("<<<< Oracle VM")):
		return "vdi", nil
	case n >= 32773 && bytes.Equal(header[32769:32774], []byte("CD001")):
		return "iso", nil
	}

	// Check if it's a tar by trying to parse it
	file.Seek(0, 0)
	tarReader := tar.NewReader(file)
	if _, err := tarReader.Next(); err == nil {
		return "tar", nil
	}

	// Check if text file (Dockerfile)
	isText := true
	for _, b := range header {
		if b < 0x09 || (b > 0x0d && b < 0x20 && b != 0x1b) {
			if b > 0x7e {
				isText = false
				break
			}
		}
	}
	if isText && bytes.Contains(header, []byte("FROM")) {
		return "dockerfile", nil
	}

	return "unknown", nil
}

// scanForMalware scans a file using ClamAV
func (v *Validator) scanForMalware(ctx context.Context, filePath string) *MalwareScanResult {
	result := &MalwareScanResult{
		Scanned:     false,
		Clean:       true,
		ScannerUsed: "clamav",
	}

	if !v.clamavEnabled {
		result.Error = "ClamAV not available"
		return result
	}

	// Use clamscan command
	cmd := exec.CommandContext(ctx, "clamscan", "--no-summary", "--infected", filePath)
	output, err := cmd.CombinedOutput()

	result.Scanned = true

	if err != nil {
		// Exit code 1 = virus found, 2 = error
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// Virus found
				result.Clean = false
				lines := strings.Split(string(output), "\n")
				for _, line := range lines {
					if strings.Contains(line, "FOUND") {
						result.Threats = append(result.Threats, strings.TrimSpace(line))
					}
				}
			} else {
				result.Error = fmt.Sprintf("scan error: %s", string(output))
			}
		} else {
			result.Error = err.Error()
		}
	}

	return result
}

// checkClamAV checks if ClamAV is available
func (v *Validator) checkClamAV() bool {
	cmd := exec.Command("clamscan", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// quarantineFile moves a suspicious file to quarantine
func (v *Validator) quarantineFile(filePath string) error {
	filename := filepath.Base(filePath)
	checksum, _ := v.calculateChecksum(filePath)
	quarantineName := fmt.Sprintf("%s_%s", checksum[:8], filename)
	quarantinePath := filepath.Join(v.config.QuarantinePath, quarantineName)

	return os.Rename(filePath, quarantinePath)
}

// ValidateDockerfileSecurity performs security-focused Dockerfile analysis
func (v *Validator) ValidateDockerfileSecurity(content []byte) []string {
	var issues []string

	// Check for running as root
	if !regexp.MustCompile(`(?im)^USER\s+`).Match(content) {
		issues = append(issues, "Dockerfile does not specify a USER, container will run as root")
	}

	// Check for latest tag
	if regexp.MustCompile(`(?im)^FROM\s+\S+:latest`).Match(content) {
		issues = append(issues, "Using 'latest' tag is not recommended, pin to specific version")
	}

	// Check for privileged operations
	if regexp.MustCompile(`(?im)(--privileged|--cap-add)`).Match(content) {
		issues = append(issues, "Dockerfile may require privileged operations")
	}

	// Check for sensitive mounts
	if regexp.MustCompile(`(?im)(-v\s+/var/run/docker\.sock|-v\s+/etc/shadow)`).Match(content) {
		issues = append(issues, "Dockerfile may mount sensitive host paths")
	}

	return issues
}
