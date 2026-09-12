package database

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestReadMigrationsSortsAndFingerprints(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/002_second.sql": {Data: []byte("SELECT 2;")},
		"migrations/001_first.sql":  {Data: []byte("SELECT 1;")},
		"migrations/README.md":      {Data: []byte("ignored")},
	}

	got, err := readMigrations(fsys, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[1].Version != 2 {
		t.Fatalf("unexpected migration order: %#v", got)
	}
	if got[0].Name != "001_first.sql" || len(got[0].Checksum) != 64 {
		t.Fatalf("unexpected migration metadata: %#v", got[0])
	}
	if got[0].Checksum == got[1].Checksum {
		t.Fatal("different migration contents received the same checksum")
	}
}

func TestReadMigrationsRejectsDuplicateVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/001_first.sql": {Data: []byte("SELECT 1;")},
		"migrations/001_again.sql": {Data: []byte("SELECT 2;")},
	}

	_, err := readMigrations(fsys, "migrations")
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version 1") {
		t.Fatalf("expected duplicate-version error, got %v", err)
	}
}

func TestReadMigrationsRejectsGapsAndMalformedSQLNames(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{
			name: "gap",
			fsys: fstest.MapFS{
				"migrations/001_first.sql": {Data: []byte("SELECT 1;")},
				"migrations/003_third.sql": {Data: []byte("SELECT 3;")},
			},
			want: "missing migration version 2",
		},
		{
			name: "malformed",
			fsys: fstest.MapFS{
				"migrations/not-versioned.sql": {Data: []byte("SELECT 1;")},
			},
			want: "invalid migration filename",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readMigrations(tt.fsys, "migrations")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}
