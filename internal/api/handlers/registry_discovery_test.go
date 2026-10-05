package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-lab/anvil/internal/services/registryauth"
)

func TestValidateRegistryCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/auth/token":
			var login map[string]string
			_ = json.NewDecoder(r.Body).Decode(&login)
			if login["identifier"] != "docker-user" || login["secret"] != "docker-token" {
				http.Error(w, "rejected", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"docker-session"}`))
		case "/user":
			if r.Header.Get("Authorization") != "Bearer github-token" {
				http.Error(w, "rejected", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"login":"github-user"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := validateRegistryCredentialAt(context.Background(), server.Client(), "docker.io", "docker-user", "docker-token", server.URL, server.URL); err != nil {
		t.Fatalf("validate Docker Hub credential: %v", err)
	}
	if err := validateRegistryCredentialAt(context.Background(), server.Client(), "ghcr.io", "github-user", "github-token", server.URL, server.URL); err != nil {
		t.Fatalf("validate GHCR credential: %v", err)
	}
	err := validateRegistryCredentialAt(context.Background(), server.Client(), "ghcr.io", "github-user", "wrong", server.URL, server.URL)
	var apiErr *registryAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("expected rejected credential, got %v", err)
	}
}

func TestDiscoverDockerHubRepositories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/auth/token":
			var login map[string]string
			_ = json.NewDecoder(r.Body).Decode(&login)
			if login["identifier"] != "organizer" || login["secret"] != "read-token" {
				t.Fatalf("unexpected login payload: %v", login)
			}
			_, _ = w.Write([]byte(`{"access_token":"listing-token"}`))
		case "/v2/namespaces/acme/repositories":
			if r.Header.Get("Authorization") != "Bearer listing-token" || r.URL.Query().Get("page_size") != "100" {
				t.Fatalf("unexpected repository request")
			}
			_, _ = w.Write([]byte(`{"next":null,"results":[{"name":"crypto_vault","description":"Challenge image","is_private":true,"last_updated":"2026-09-30T10:00:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	credential := &registryauth.Credential{Registry: "docker.io", Username: "organizer", Secret: "read-token"}
	repositories, err := discoverDockerHubRepositories(context.Background(), server.Client(), server.URL, "acme", credential)
	if err != nil || len(repositories) != 1 {
		t.Fatalf("repositories=%v error=%v", repositories, err)
	}
	if repositories[0].Image != "acme/crypto_vault:latest" || repositories[0].SuggestedSlug != "crypto-vault" || repositories[0].Visibility != "private" {
		t.Fatalf("unexpected repository: %+v", repositories[0])
	}
}

func TestDiscoverGHCRRepositoriesFallsBackToUserOwner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer read-token" {
			t.Fatal("missing package token")
		}
		switch r.URL.Path {
		case "/orgs/abuctf/packages":
			http.NotFound(w, r)
		case "/users/abuctf/packages":
			_, _ = w.Write([]byte(`[{"name":"token-overflow","visibility":"private","updated_at":"2026-09-30T10:00:00Z"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	credential := &registryauth.Credential{Registry: "ghcr.io", Username: "abuctf", Secret: "read-token"}
	repositories, err := discoverGHCRRepositories(context.Background(), server.Client(), server.URL, "abuctf", credential)
	if err != nil || len(repositories) != 1 || repositories[0].Image != "ghcr.io/abuctf/token-overflow:latest" {
		t.Fatalf("repositories=%v error=%v", repositories, err)
	}
}
