package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/services/registryauth"
	"github.com/distribution/reference"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	"go.uber.org/zap"
)

const dockerHubAPIURL = "https://hub.docker.com"
const githubAPIURL = "https://api.github.com"

var registryNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$`)

type registryDiscoverRequest struct {
	Registry  string `json:"registry"`
	Namespace string `json:"namespace"`
}

type discoveredRepository struct {
	Name            string    `json:"name"`
	Image           string    `json:"image"`
	Description     string    `json:"description,omitempty"`
	Visibility      string    `json:"visibility"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	SuggestedSlug   string    `json:"suggested_slug"`
	AlreadyImported bool      `json:"already_imported"`
	SlugConflict    bool      `json:"slug_conflict"`
}

type registryAPIError struct {
	Status int
	Body   string
}

func (e *registryAPIError) Error() string {
	return fmt.Sprintf("registry API returned %d", e.Status)
}

func (h *AdminChallengeHandler) DiscoverRegistryRepositories(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request registryDiscoverRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "registry and namespace are required"})
		return
	}
	request.Registry = strings.ToLower(strings.TrimSpace(request.Registry))
	request.Namespace = strings.TrimSpace(request.Namespace)
	if !registryauth.SupportedRegistry(request.Registry) || !registryNamespacePattern.MatchString(request.Namespace) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "choose Docker Hub or GHCR and enter a valid namespace"})
		return
	}
	var credential *registryauth.Credential
	var err error
	if h.registrySvc != nil {
		credential, err = h.registrySvc.Get(c.Request.Context(), request.Registry)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "registry credentials cannot be decrypted"})
			return
		}
	}
	if request.Registry == "ghcr.io" && credential == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "save a read-only GHCR credential before discovering GitHub container packages"})
		return
	}
	client := &http.Client{Timeout: 20 * time.Second}
	var repositories []discoveredRepository
	if request.Registry == "docker.io" {
		repositories, err = discoverDockerHubRepositories(c.Request.Context(), client, dockerHubAPIURL, request.Namespace, credential)
	} else {
		repositories, err = discoverGHCRRepositories(c.Request.Context(), client, githubAPIURL, request.Namespace, credential)
	}
	if err != nil {
		var apiErr *registryAPIError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case http.StatusUnauthorized, http.StatusForbidden:
				c.JSON(http.StatusForbidden, gin.H{"error": "the namespace is private or the saved read-only credential cannot list it"})
			case http.StatusNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "the registry namespace was not found"})
			case http.StatusTooManyRequests:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "the registry rate limit was reached; retry later"})
			default:
				c.JSON(http.StatusBadGateway, gin.H{"error": "the registry could not list repositories"})
			}
			return
		}
		h.logger.Warn("discover registry repositories", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "the registry could not list repositories"})
		return
	}
	h.markImportedRepositories(c.Request.Context(), repositories)
	c.JSON(http.StatusOK, gin.H{"registry": request.Registry, "namespace": request.Namespace, "repositories": repositories, "credential_used": credential != nil, "truncated": len(repositories) >= 500})
}

func discoverDockerHubRepositories(ctx context.Context, client *http.Client, apiBase, namespace string, credential *registryauth.Credential) ([]discoveredRepository, error) {
	token := ""
	if credential != nil {
		payload, _ := json.Marshal(map[string]string{"identifier": credential.Username, "secret": credential.Secret})
		var result struct {
			AccessToken string `json:"access_token"`
			Token       string `json:"token"`
		}
		if err := registryAPIRequest(ctx, client, http.MethodPost, strings.TrimRight(apiBase, "/")+"/v2/auth/token", payload, "", &result); err != nil {
			return nil, err
		}
		token = result.AccessToken
		if token == "" {
			token = result.Token
		}
		if token == "" {
			return nil, errors.New("Docker Hub returned an empty access token")
		}
	}
	repositories := []discoveredRepository{}
	for page := 1; page <= 5; page++ {
		endpoint := fmt.Sprintf("%s/v2/namespaces/%s/repositories?page=%d&page_size=100&ordering=name", strings.TrimRight(apiBase, "/"), url.PathEscape(namespace), page)
		var result struct {
			Next    string `json:"next"`
			Results []struct {
				Name        string    `json:"name"`
				Description string    `json:"description"`
				IsPrivate   bool      `json:"is_private"`
				UpdatedAt   time.Time `json:"last_updated"`
			} `json:"results"`
		}
		if err := registryAPIRequest(ctx, client, http.MethodGet, endpoint, nil, token, &result); err != nil {
			return nil, err
		}
		for _, item := range result.Results {
			visibility := "public"
			if item.IsPrivate {
				visibility = "private"
			}
			image := strings.ToLower(namespace) + "/" + item.Name + ":latest"
			repositories = append(repositories, discoveredRepository{Name: item.Name, Image: image, Description: item.Description, Visibility: visibility, UpdatedAt: item.UpdatedAt, SuggestedSlug: repositorySlug(item.Name)})
		}
		if result.Next == "" || len(result.Results) == 0 {
			break
		}
	}
	return repositories, nil
}

func discoverGHCRRepositories(ctx context.Context, client *http.Client, apiBase, namespace string, credential *registryauth.Credential) ([]discoveredRepository, error) {
	token := ""
	if credential != nil {
		token = credential.Secret
	}
	repositories, err := discoverGitHubPackages(ctx, client, apiBase, "orgs", namespace, token)
	var apiErr *registryAPIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return discoverGitHubPackages(ctx, client, apiBase, "users", namespace, token)
	}
	return repositories, err
}

func discoverGitHubPackages(ctx context.Context, client *http.Client, apiBase, ownerType, namespace, token string) ([]discoveredRepository, error) {
	repositories := []discoveredRepository{}
	for page := 1; page <= 5; page++ {
		endpoint := fmt.Sprintf("%s/%s/%s/packages?package_type=container&per_page=100&page=%d", strings.TrimRight(apiBase, "/"), ownerType, url.PathEscape(namespace), page)
		var result []struct {
			Name       string    `json:"name"`
			Visibility string    `json:"visibility"`
			UpdatedAt  time.Time `json:"updated_at"`
		}
		if err := registryAPIRequest(ctx, client, http.MethodGet, endpoint, nil, token, &result); err != nil {
			return nil, err
		}
		for _, item := range result {
			image := "ghcr.io/" + strings.ToLower(namespace) + "/" + item.Name + ":latest"
			repositories = append(repositories, discoveredRepository{Name: item.Name, Image: image, Visibility: item.Visibility, UpdatedAt: item.UpdatedAt, SuggestedSlug: repositorySlug(item.Name)})
		}
		if len(result) < 100 {
			break
		}
	}
	return repositories, nil
}

func registryAPIRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte, token string, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json, application/json")
	request.Header.Set("User-Agent", "Anvil-Registry-Importer/1")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasPrefix(endpoint, "https://api.github.com/") {
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return &registryAPIError{Status: response.StatusCode, Body: string(message)}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}

func repositorySlug(name string) string {
	value := slug.Make(strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(name))
	if len(value) > 100 {
		value = strings.Trim(value[:100], "-")
	}
	if value == "" {
		return "container-challenge"
	}
	return value
}

func (h *AdminChallengeHandler) markImportedRepositories(ctx context.Context, repositories []discoveredRepository) {
	rows, err := h.db.Pool.Query(ctx, `SELECT slug, container_image FROM challenges`)
	if err != nil {
		return
	}
	defer rows.Close()
	slugs := map[string]bool{}
	images := map[string]bool{}
	for rows.Next() {
		var challengeSlug, image string
		if rows.Scan(&challengeSlug, &image) != nil {
			return
		}
		slugs[challengeSlug] = true
		if named, parseErr := reference.ParseNormalizedNamed(image); parseErr == nil {
			images[reference.Domain(named)+"/"+reference.Path(named)] = true
		}
	}
	for index := range repositories {
		repositories[index].SlugConflict = slugs[repositories[index].SuggestedSlug]
		if named, parseErr := reference.ParseNormalizedNamed(repositories[index].Image); parseErr == nil {
			repositories[index].AlreadyImported = images[reference.Domain(named)+"/"+reference.Path(named)]
		}
	}
}
