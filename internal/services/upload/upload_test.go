package upload

import (
	"strings"
	"testing"
)

func TestGenerateStorageKeyStripsFilenamePaths(t *testing.T) {
	for _, filename := range []string{"../../payload.ova", `..\..\payload.ova`} {
		key := generateStorageKey("user", FileTypeOVA, "upload", filename)
		if key != "vms/user/upload/payload.ova" {
			t.Fatalf("generateStorageKey(%q) = %q", filename, key)
		}
		if strings.Contains(key, "..") {
			t.Fatalf("generateStorageKey(%q) retained traversal segments: %q", filename, key)
		}
	}
}
