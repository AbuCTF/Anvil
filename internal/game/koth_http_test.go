package game

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPProbeControllerHeldAndUnheld(t *testing.T) {
	body := `{"holder":"koth_abc","since":123}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/koth/status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := legacyHTTPProbe{client: srv.Client(), baseURL: srv.URL}

	if got, err := p.controller(context.Background(), Target{}); err != nil || got != "koth_abc" {
		t.Fatalf("held: got %q err=%v want koth_abc", got, err)
	}
	// null holder => unheld => empty string, not "null"
	body = `{"holder":null,"since":0}`
	if got, err := p.controller(context.Background(), Target{}); err != nil || got != "" {
		t.Fatalf("unheld: got %q err=%v want empty", got, err)
	}
}

func TestHTTPProbeResetHonorsBearer(t *testing.T) {
	const secret = "s3cr3t"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/koth/reset" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := (legacyHTTPProbe{client: srv.Client(), baseURL: srv.URL, resetSecret: secret}).reset(context.Background(), Target{}, 1); err != nil {
		t.Fatalf("reset with good bearer: %v", err)
	}
	if err := (legacyHTTPProbe{client: srv.Client(), baseURL: srv.URL, resetSecret: "wrong"}).reset(context.Background(), Target{}, 1); err == nil {
		t.Fatal("reset with bad bearer: want error, got nil")
	}
}

func TestHillV2HTTPProbeStatusAndReset(t *testing.T) {
	const resetToken = "reset-private-sentinel"
	status := `{"hill":"current-affairs","round":7,"started_ms":1700000000000,"ends_ms":1700000900000,"holder":"team-a","open_door_count":3,"locked_door_count":1,"pending_lock":false,"events":[]}`
	var resetCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			_, _ = w.Write([]byte(status))
		case r.Method == http.MethodPost && r.URL.Path == "/reset":
			resetCalls++
			if r.Header.Get("X-Reset-Token") != resetToken || r.Header.Get("Idempotency-Key") != "anvil:round:00000042" || r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"hill":"current-affairs","round":8,"started_ms":1700000900000,"ends_ms":1700001800000,"holder":null,"open_door_count":3,"locked_door_count":1,"pending_lock":false,"events":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := hillV2HTTPProbe{client: srv.Client(), baseURL: srv.URL, resetSecret: resetToken, hillSlug: "current-affairs"}
	if got, err := p.controller(context.Background(), Target{}); err != nil || got != "team-a" {
		t.Fatalf("controller = %q err=%v, want team-a", got, err)
	}
	if err := p.reset(context.Background(), Target{}, 42); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if resetCalls != 1 {
		t.Fatalf("reset calls = %d, want 1", resetCalls)
	}
}

func TestHillV2HTTPProbeRejectsInvalidOrSensitiveResponses(t *testing.T) {
	secret := "H7CTF{0123456789abcdef0123456789abcdef}"
	body := `{"hill":"current-affairs","round":7,"started_ms":1,"ends_ms":2,"holder":"this-holder-name-is-far-too-long-for-the-contract","open_door_count":3,"locked_door_count":0,"pending_lock":false,"events":[],"private":"` + secret + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := hillV2HTTPProbe{client: srv.Client(), baseURL: srv.URL, resetSecret: "reset", hillSlug: "current-affairs"}
	got, err := p.controller(context.Background(), Target{})
	if got != "" || err == nil {
		t.Fatalf("invalid response = %q err=%v", got, err)
	}
	var safe safeProbeError
	if !errors.As(err, &safe) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), body) {
		t.Fatalf("diagnostic was not redacted: %v", err)
	}
	if err := p.reset(context.Background(), Target{}, 0); err == nil {
		t.Fatalf("invalid round diagnostic = %v", err)
	}
}

func TestHillV2ResetKeyDeterministic(t *testing.T) {
	first, err := hillV2ResetKey(9)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hillV2ResetKey(9)
	if err != nil || first != second || first != "anvil:round:00000009" {
		t.Fatalf("reset keys = %q %q err=%v", first, second, err)
	}
}

func TestHillV2ResetRejectsUnchangedHeldRound(t *testing.T) {
	status := `{"hill":"current-affairs","round":7,"started_ms":1700000000000,"ends_ms":1700000900000,"holder":"team-a","open_door_count":3,"locked_door_count":0,"pending_lock":false,"events":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(status))
	}))
	defer srv.Close()

	p := hillV2HTTPProbe{client: srv.Client(), baseURL: srv.URL, resetSecret: "reset", hillSlug: "current-affairs"}
	if err := p.reset(context.Background(), Target{}, 8); err == nil || err.Error() != "hill reset round_mismatch" {
		t.Fatalf("unchanged held round reset = %v", err)
	}
}
