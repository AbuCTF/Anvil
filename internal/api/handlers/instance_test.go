package handlers

import (
	"net/http"
	"testing"
)

func TestParseBoundedPositiveInt(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		minimum int
		maximum int
		want    int
		wantErr bool
	}{
		{name: "minimum", raw: "1", minimum: 1, maximum: 100, want: 1},
		{name: "maximum", raw: "100", minimum: 1, maximum: 100, want: 100},
		{name: "whitespace", raw: " 30 ", minimum: 1, maximum: 1440, want: 30},
		{name: "zero", raw: "0", minimum: 1, maximum: 100, wantErr: true},
		{name: "negative", raw: "-1", minimum: 1, maximum: 100, wantErr: true},
		{name: "too large", raw: "101", minimum: 1, maximum: 100, wantErr: true},
		{name: "not integer", raw: "2.5", minimum: 1, maximum: 100, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBoundedPositiveInt(tt.raw, tt.minimum, tt.maximum)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse setting: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDynamicFlagEnvName(t *testing.T) {
	tests := map[string]string{
		"User Flag":      "FLAG_USER_FLAG",
		"root-shell":     "FLAG_ROOT_SHELL",
		"Token/Overflow": "FLAG_TOKEN_OVERFLOW",
		"already_OK":     "FLAG_ALREADY_OK",
	}

	for input, want := range tests {
		if got := dynamicFlagEnvName(input); got != want {
			t.Errorf("dynamicFlagEnvName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBooleanSettingValues(t *testing.T) {
	for _, raw := range []string{"true", " false ", "TRUE"} {
		if _, err := parseBooleanSetting(raw); err != nil {
			t.Errorf("expected %q to parse as a boolean: %v", raw, err)
		}
	}
	if _, err := parseBooleanSetting("enabled"); err == nil {
		t.Fatal("expected a non-boolean setting value to fail")
	}
}

func TestValidateRevertState(t *testing.T) {
	runtimeID := "runtime-1"
	emptyRuntimeID := "  "
	tests := []struct {
		name       string
		status     string
		resetCount int
		maxResets  int
		runtimeID  *string
		wantStatus int
		wantError  string
	}{
		{name: "eligible", status: "running", maxResets: 3, runtimeID: &runtimeID},
		{name: "not running", status: "stopping", maxResets: 3, runtimeID: &runtimeID, wantStatus: http.StatusConflict, wantError: "instance is not running"},
		{name: "limit reached", status: "running", resetCount: 3, maxResets: 3, runtimeID: &runtimeID, wantStatus: http.StatusConflict, wantError: "reset limit reached"},
		{name: "resets disabled", status: "running", maxResets: 0, runtimeID: &runtimeID, wantStatus: http.StatusConflict, wantError: "reset limit reached"},
		{name: "negative count", status: "running", resetCount: -1, maxResets: 3, runtimeID: &runtimeID, wantStatus: http.StatusInternalServerError, wantError: "instance has an invalid reset limit"},
		{name: "negative limit", status: "running", maxResets: -1, runtimeID: &runtimeID, wantStatus: http.StatusInternalServerError, wantError: "instance has an invalid reset limit"},
		{name: "missing runtime", status: "running", maxResets: 3, wantStatus: http.StatusConflict, wantError: "instance runtime is unavailable"},
		{name: "blank runtime", status: "running", maxResets: 3, runtimeID: &emptyRuntimeID, wantStatus: http.StatusConflict, wantError: "instance runtime is unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRevertState(tt.status, tt.resetCount, tt.maxResets, tt.runtimeID)
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("validateRevertState() error = %#v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateRevertState() returned nil error")
			}
			if err.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", err.status, tt.wantStatus)
			}
			if got := err.body["error"]; got != tt.wantError {
				t.Fatalf("error = %#v, want %q", got, tt.wantError)
			}
			if tt.name == "limit reached" {
				if err.body["reset_count"] != 3 || err.body["max_resets"] != 3 {
					t.Fatalf("limit details = %#v", err.body)
				}
			}
		})
	}
}
