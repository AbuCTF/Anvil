package game

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func probeTarget(t *testing.T, raw string) Target {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	return Target{Host: u.Hostname(), Port: port}
}

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

	p := httpProbe{client: srv.Client()}
	tgt := probeTarget(t, srv.URL)

	if got := p.controller(context.Background(), tgt); got != "koth_abc" {
		t.Fatalf("held: got %q want koth_abc", got)
	}
	// null holder => unheld => empty string, not "null"
	body = `{"holder":null,"since":0}`
	if got := p.controller(context.Background(), tgt); got != "" {
		t.Fatalf("unheld: got %q want empty", got)
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
	tgt := probeTarget(t, srv.URL)

	if err := (httpProbe{client: srv.Client(), resetSecret: secret}).reset(context.Background(), tgt); err != nil {
		t.Fatalf("reset with good bearer: %v", err)
	}
	if err := (httpProbe{client: srv.Client(), resetSecret: "wrong"}).reset(context.Background(), tgt); err == nil {
		t.Fatal("reset with bad bearer: want error, got nil")
	}
}
