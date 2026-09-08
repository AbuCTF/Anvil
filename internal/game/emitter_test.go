package game

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"go.uber.org/zap"
)

func TestPostSignedVerifies(t *testing.T) {
	const secret = "topsecret"
	const id = "target-1"
	body := []byte(`{"tick":7}`)

	var gotSig, gotTS string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTS = r.Header.Get("X-RCTF-Timestamp")
		gotSig = r.Header.Get("X-RCTF-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Controller{
		cfg:        config.GameConfig{Webhook: config.WebhookConfig{Enabled: true, URL: srv.URL, Secret: secret, ID: id}},
		emitClient: &http.Client{},
		logger:     zap.NewNop(),
	}
	if err := c.postSigned(context.Background(), body); err != nil {
		t.Fatalf("postSigned: %v", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s.%s", gotTS, id, gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Errorf("signature mismatch:\n got %s\nwant %s", gotSig, want)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body mismatch: got %s want %s", gotBody, body)
	}
}

func TestPostSignedNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Controller{
		cfg:        config.GameConfig{Webhook: config.WebhookConfig{URL: srv.URL, Secret: "s"}},
		emitClient: &http.Client{},
		logger:     zap.NewNop(),
	}
	if err := c.postSigned(context.Background(), []byte("{}")); err == nil {
		t.Fatal("expected error on non-2xx")
	}
}
