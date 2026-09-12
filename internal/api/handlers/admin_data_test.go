package handlers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPrepareNewFlag(t *testing.T) {
	tests := []struct {
		name     string
		req      CreateFlagRequest
		wantType string
		wantHash string
		wantErr  string
	}{
		{name: "static default", req: CreateFlagRequest{Flag: "ANVIL{ok}"}, wantType: "static", wantHash: hashFlag("ANVIL{ok}")},
		{name: "case insensitive static", req: CreateFlagRequest{Flag: "ANVIL{Ok}", CaseSensitive: boolPointer(false)}, wantType: "static", wantHash: hashFlag("anvil{ok}")},
		{name: "regex", req: CreateFlagRequest{FlagType: "regex", Flag: `^ANVIL\{[a-z]+\}$`}, wantType: "regex", wantHash: `^ANVIL\{[a-z]+\}$`},
		{name: "dynamic", req: CreateFlagRequest{FlagType: "dynamic"}, wantType: "dynamic"},
		{name: "unknown type", req: CreateFlagRequest{FlagType: "script", Flag: "x"}, wantErr: "flag_type"},
		{name: "empty static", req: CreateFlagRequest{}, wantErr: "flag is required"},
		{name: "invalid regex", req: CreateFlagRequest{FlagType: "regex", Flag: "["}, wantErr: "invalid regex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotHash, err := prepareNewFlag(tt.req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected %q error, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepare flag: %v", err)
			}
			if gotType != tt.wantType || gotHash != tt.wantHash {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotType, gotHash, tt.wantType, tt.wantHash)
			}
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestGenerateOpaqueToken(t *testing.T) {
	first, err := generateOpaqueToken("anvil_team_")
	if err != nil {
		t.Fatalf("generate first token: %v", err)
	}
	second, err := generateOpaqueToken("anvil_team_")
	if err != nil {
		t.Fatalf("generate second token: %v", err)
	}
	if first == second {
		t.Fatal("generated duplicate tokens")
	}
	if !strings.HasPrefix(first, "anvil_team_") || len(first) > 50 {
		t.Fatalf("token does not fit schema: %q", first)
	}
}

func TestValidateTokenLifetime(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Minute)
	if err := validateTokenLifetime(0, nil); err == nil {
		t.Fatal("accepted zero uses")
	}
	if err := validateTokenLifetime(1, &past); err == nil {
		t.Fatal("accepted expired token")
	}
	if err := validateTokenLifetime(1, &future); err != nil {
		t.Fatalf("rejected valid token: %v", err)
	}
}

func TestParsePage(t *testing.T) {
	limit, offset, err := parsePage("25", "50", 100, 200)
	if err != nil || limit != 25 || offset != 50 {
		t.Fatalf("unexpected page: limit=%d offset=%d err=%v", limit, offset, err)
	}
	for _, values := range [][2]string{{"0", "0"}, {"201", "0"}, {"x", "0"}, {"10", "-1"}} {
		if _, _, err := parsePage(values[0], values[1], 100, 200); err == nil {
			t.Fatalf("accepted invalid page %q/%q", values[0], values[1])
		}
	}
}

func TestPostgresErrorCodeUnwrapsConstraintErrors(t *testing.T) {
	err := fmt.Errorf("insert category: %w", &pgconn.PgError{Code: "23505"})
	if got := postgresErrorCode(err); got != "23505" {
		t.Fatalf("code = %q, want 23505", got)
	}
	if got := postgresErrorCode(fmt.Errorf("ordinary failure")); got != "" {
		t.Fatalf("ordinary error code = %q, want empty", got)
	}
}
