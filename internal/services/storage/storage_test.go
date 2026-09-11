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
