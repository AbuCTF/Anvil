package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-lab/anvil/internal/services/registryauth"
)

func TestInspectDockerHubSingleManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.URL.Query().Get("scope") != "repository:acme/challenge:pull" {
				t.Fatalf("unexpected scope %q", r.URL.Query().Get("scope"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"test-token"}`))
		case "/v2/acme/challenge/manifests/stable":
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatal("missing registry bearer token")
			}
			w.Header().Set("Docker-Content-Digest", "sha256:manifest")
			w.Header().Set("RateLimit-Remaining", "99;w=21600")
			_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:config"},"layers":[{"size":120},{"size":30}]}`))
		case "/v2/acme/challenge/blobs/sha256:config":
			_, _ = w.Write([]byte(`{"os":"linux","architecture":"amd64"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := inspectRegistryImage(context.Background(), server.Client(), server.URL+"/token", server.URL, "registry.docker.io", "Docker Hub", "docker.io", "acme/challenge:stable", "linux/amd64")
	if err != nil {
		t.Fatalf("inspect image: %v", err)
	}
	if result.Repository != "acme/challenge" || result.Digest != "sha256:manifest" {
		t.Fatalf("unexpected image result: %#v", result)
	}
	if result.ImmutableReference != "docker.io/acme/challenge@sha256:manifest" {
		t.Fatalf("unexpected immutable reference %q", result.ImmutableReference)
	}
	if result.SizeBytes != 150 || result.SelectedPlatform == nil || result.SelectedPlatform.Architecture != "amd64" {
		t.Fatalf("unexpected platform result: %#v", result)
	}
	if result.RateLimitRemaining != "99;w=21600" {
		t.Fatalf("unexpected rate limit %q", result.RateLimitRemaining)
	}
}

func TestInspectDockerHubManifestIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case "/v2/library/example/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:index")
			_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}},{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64","variant":"v8"}},{"digest":"sha256:windows","platform":{"os":"windows","architecture":"amd64"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := inspectRegistryImage(context.Background(), server.Client(), server.URL+"/token", server.URL, "registry.docker.io", "Docker Hub", "docker.io", "example", "linux/arm64")
	if err != nil {
		t.Fatalf("inspect image: %v", err)
	}
	if len(result.Platforms) != 3 || result.SelectedPlatform == nil || result.SelectedPlatform.Digest != "sha256:arm" {
		t.Fatalf("unexpected platforms: %#v", result.Platforms)
	}
}

func TestInspectGHCRManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.URL.Query().Get("service") != "ghcr.io" || r.URL.Query().Get("scope") != "repository:abuctf/token-overflow:pull" {
				t.Fatalf("unexpected GHCR token query %q", r.URL.RawQuery)
			}
			username, password, ok := r.BasicAuth()
			if !ok || username != "abuctf" || password != "read-token" {
				t.Fatal("missing GHCR registry credential")
			}
			_, _ = w.Write([]byte(`{"token":"ghcr-token"}`))
		case "/v2/abuctf/token-overflow/manifests/latest":
			if r.Header.Get("Authorization") != "Bearer ghcr-token" {
				t.Fatal("missing GHCR bearer token")
			}
			w.Header().Set("Docker-Content-Digest", "sha256:ghcr-index")
			_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	credential := &registryauth.Credential{Registry: "ghcr.io", Username: "abuctf", Secret: "read-token"}
	result, err := inspectRegistryImageWithCredential(context.Background(), server.Client(), server.URL+"/token", server.URL, "ghcr.io", "GitHub Container Registry", "ghcr.io", "ghcr.io/abuctf/token-overflow:latest", "linux/amd64", credential)
	if err != nil {
		t.Fatalf("inspect GHCR image: %v", err)
	}
	if result.Registry != "ghcr.io" || result.ImmutableReference != "ghcr.io/abuctf/token-overflow@sha256:ghcr-index" {
		t.Fatalf("unexpected GHCR result: %#v", result)
	}
}

func TestInspectDockerHubRejectsUnsupportedInputs(t *testing.T) {
	client := &http.Client{}
	if _, err := inspectRegistryImage(context.Background(), client, "http://unused", "http://unused", "registry.docker.io", "Docker Hub", "docker.io", "registry.example.com/acme/challenge:latest", ""); err == nil {
		t.Fatal("expected registry mismatch rejection")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"token":"test-token"}`))
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:index")
		_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}}]}`))
	}))
	defer server.Close()

	_, err := inspectRegistryImage(context.Background(), server.Client(), server.URL+"/token", server.URL, "registry.docker.io", "Docker Hub", "docker.io", "acme/challenge:latest", "linux/amd64")
	var statusErr *registryStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("expected platform mismatch, got %v", err)
	}
}
