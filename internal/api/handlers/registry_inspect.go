package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/services/registryauth"
	"github.com/distribution/reference"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const dockerHubAuthURL = "https://auth.docker.io/token"
const dockerHubRegistryURL = "https://registry-1.docker.io"
const ghcrAuthURL = "https://ghcr.io/token"
const ghcrRegistryURL = "https://ghcr.io"

var registryAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

type registryInspectRequest struct {
	Image    string `json:"image"`
	Platform string `json:"platform"`
}

type registryPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
	Digest       string `json:"digest,omitempty"`
}

type registryInspection struct {
	Registry           string             `json:"registry"`
	Repository         string             `json:"repository"`
	Reference          string             `json:"reference"`
	Digest             string             `json:"digest"`
	ImmutableReference string             `json:"immutable_reference"`
	MediaType          string             `json:"media_type"`
	SizeBytes          int64              `json:"size_bytes,omitempty"`
	Platforms          []registryPlatform `json:"platforms"`
	SelectedPlatform   *registryPlatform  `json:"selected_platform,omitempty"`
	RateLimitRemaining string             `json:"rate_limit_remaining,omitempty"`
}

type registryManifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		} `json:"platform"`
	} `json:"manifests"`
}

type registryConfig struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

func (h *AdminChallengeHandler) ListRegistryCredentials(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT registry, username, created_at, updated_at FROM registry_credentials ORDER BY registry`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load registry credentials"})
		return
	}
	defer rows.Close()
	credentials := []gin.H{}
	for rows.Next() {
		var registry, username string
		var createdAt, updatedAt time.Time
		if rows.Scan(&registry, &username, &createdAt, &updatedAt) != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load registry credentials"})
			return
		}
		credentials = append(credentials, gin.H{"registry": registry, "username": username, "secret_configured": true, "created_at": createdAt, "updated_at": updatedAt})
	}
	if rows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load registry credentials"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"credentials": credentials})
}

func (h *AdminChallengeHandler) SaveRegistryCredential(c *gin.Context) {
	if h.registrySvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "registry credential storage is unavailable"})
		return
	}
	registry := strings.ToLower(strings.TrimSpace(c.Param("registry")))
	if !registryauth.SupportedRegistry(registry) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "registry must be docker.io or ghcr.io"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and access token are required"})
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if err := h.registrySvc.Save(c.Request.Context(), registry, request.Username, request.Token, uid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "registry_credential_saved", "registry_credential", "", map[string]any{"registry": registry, "username": strings.TrimSpace(request.Username)})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AdminChallengeHandler) DeleteRegistryCredential(c *gin.Context) {
	if h.registrySvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "registry credential storage is unavailable"})
		return
	}
	registry := strings.ToLower(strings.TrimSpace(c.Param("registry")))
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	deleted, err := h.registrySvc.Delete(c.Request.Context(), registry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove registry credentials"})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry credentials not found"})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "registry_credential_deleted", "registry_credential", "", map[string]any{"registry": registry})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AdminChallengeHandler) InspectRegistryImage(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var request registryInspectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image is required"})
		return
	}
	request.Image = strings.TrimSpace(request.Image)
	request.Platform = strings.TrimSpace(request.Platform)
	if request.Image == "" || len(request.Image) > 512 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image must be between 1 and 512 characters"})
		return
	}
	if request.Platform != "" && request.Platform != "linux/amd64" && request.Platform != "linux/arm64" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "platform must be linux/amd64 or linux/arm64"})
		return
	}
	named, err := reference.ParseNormalizedNamed(request.Image)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid registry image reference"})
		return
	}
	var authURL, registryURL, service, label string
	switch reference.Domain(named) {
	case "docker.io":
		authURL, registryURL, service, label = dockerHubAuthURL, dockerHubRegistryURL, "registry.docker.io", "Docker Hub"
	case "ghcr.io":
		authURL, registryURL, service, label = ghcrAuthURL, ghcrRegistryURL, "ghcr.io", "GitHub Container Registry"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "the inspector supports Docker Hub and GHCR images"})
		return
	}
	client := &http.Client{Timeout: 12 * time.Second}
	var credential *registryauth.Credential
	if h.registrySvc != nil {
		credential, err = h.registrySvc.Get(c.Request.Context(), reference.Domain(named))
		if err != nil {
			h.logger.Error("load registry credential", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "registry credentials cannot be decrypted"})
			return
		}
	}
	inspection, err := inspectRegistryImageWithCredential(c.Request.Context(), client, authURL, registryURL, service, label, reference.Domain(named), request.Image, request.Platform, credential)
	if err != nil {
		var statusErr *registryStatusError
		if errors.As(err, &statusErr) {
			c.JSON(statusErr.Status, gin.H{"error": statusErr.Message})
			return
		}
		h.logger.Warn("inspect registry image", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": label + " could not be reached; retry or verify the image name"})
		return
	}
	c.JSON(http.StatusOK, inspection)
}

type registryStatusError struct {
	Status  int
	Message string
}

func (e *registryStatusError) Error() string {
	return e.Message
}

func inspectRegistryImage(ctx context.Context, client *http.Client, authBase, registryBase, service, label, expectedDomain, input, requestedPlatform string) (*registryInspection, error) {
	return inspectRegistryImageWithCredential(ctx, client, authBase, registryBase, service, label, expectedDomain, input, requestedPlatform, nil)
}

func inspectRegistryImageWithCredential(ctx context.Context, client *http.Client, authBase, registryBase, service, label, expectedDomain, input, requestedPlatform string, credential *registryauth.Credential) (*registryInspection, error) {
	named, err := reference.ParseNormalizedNamed(input)
	if err != nil {
		return nil, &registryStatusError{Status: http.StatusBadRequest, Message: "invalid Docker image reference"}
	}
	if reference.Domain(named) != expectedDomain {
		return nil, &registryStatusError{Status: http.StatusBadRequest, Message: "image registry does not match the selected provider"}
	}
	named = reference.TagNameOnly(named)
	repository := reference.Path(named)
	manifestReference := "latest"
	if canonical, ok := named.(reference.Canonical); ok {
		manifestReference = canonical.Digest().String()
	} else if tagged, ok := named.(reference.Tagged); ok {
		manifestReference = tagged.Tag()
	}

	tokenURL, err := url.Parse(authBase)
	if err != nil {
		return nil, err
	}
	query := tokenURL.Query()
	query.Set("service", service)
	query.Set("scope", "repository:"+repository+":pull")
	tokenURL.RawQuery = query.Encode()
	var tokenResponse struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := registryJSONRequest(ctx, client, tokenURL.String(), "", "application/json", 1<<20, &tokenResponse, nil, credential); err != nil {
		return nil, err
	}
	token := tokenResponse.Token
	if token == "" {
		token = tokenResponse.AccessToken
	}
	if token == "" {
		return nil, &registryStatusError{Status: http.StatusBadGateway, Message: label + " did not issue a pull token"}
	}

	manifestURL := strings.TrimRight(registryBase, "/") + "/v2/" + repository + "/manifests/" + url.PathEscape(manifestReference)
	var manifest registryManifest
	headers := http.Header{}
	if err := registryJSONRequest(ctx, client, manifestURL, token, registryAccept, 4<<20, &manifest, headers, nil); err != nil {
		return nil, err
	}
	digest := headers.Get("Docker-Content-Digest")
	if digest == "" {
		return nil, &registryStatusError{Status: http.StatusBadGateway, Message: label + " returned a manifest without a content digest"}
	}
	inspection := &registryInspection{
		Registry: expectedDomain, Repository: repository, Reference: manifestReference,
		Digest: digest, ImmutableReference: expectedDomain + "/" + repository + "@" + digest,
		MediaType: manifest.MediaType, Platforms: []registryPlatform{},
		RateLimitRemaining: headers.Get("RateLimit-Remaining"),
	}
	for _, descriptor := range manifest.Manifests {
		if descriptor.Platform.OS == "" || descriptor.Platform.Architecture == "" {
			continue
		}
		inspection.Platforms = append(inspection.Platforms, registryPlatform{
			OS: descriptor.Platform.OS, Architecture: descriptor.Platform.Architecture,
			Variant: descriptor.Platform.Variant, Digest: descriptor.Digest,
		})
	}
	if len(inspection.Platforms) == 0 {
		for _, layer := range manifest.Layers {
			inspection.SizeBytes += layer.Size
		}
		if manifest.Config.Digest == "" {
			return nil, &registryStatusError{Status: http.StatusUnprocessableEntity, Message: label + " returned an unsupported manifest"}
		}
		configURL := strings.TrimRight(registryBase, "/") + "/v2/" + repository + "/blobs/" + url.PathEscape(manifest.Config.Digest)
		var config registryConfig
		if err := registryJSONRequest(ctx, client, configURL, token, "application/octet-stream, application/json", 4<<20, &config, nil, nil); err != nil {
			return nil, err
		}
		inspection.Platforms = append(inspection.Platforms, registryPlatform{OS: config.OS, Architecture: config.Architecture, Variant: config.Variant, Digest: digest})
	}
	for index := range inspection.Platforms {
		platform := &inspection.Platforms[index]
		if requestedPlatform == "" {
			if platform.OS == "linux" && (platform.Architecture == "amd64" || platform.Architecture == "arm64") {
				copy := *platform
				inspection.SelectedPlatform = &copy
				break
			}
			continue
		}
		if platform.OS+"/"+platform.Architecture == requestedPlatform {
			copy := *platform
			inspection.SelectedPlatform = &copy
			break
		}
	}
	if inspection.SelectedPlatform == nil {
		if requestedPlatform != "" {
			return nil, &registryStatusError{Status: http.StatusUnprocessableEntity, Message: "the image does not provide " + requestedPlatform}
		}
		return nil, &registryStatusError{Status: http.StatusUnprocessableEntity, Message: "the image does not provide a supported Linux amd64 or arm64 build"}
	}
	return inspection, nil
}

func registryJSONRequest(ctx context.Context, client *http.Client, endpoint, token, accept string, limit int64, target any, responseHeaders http.Header, credential *registryauth.Credential) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "Anvil-Registry-Inspector/1")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	} else if credential != nil {
		request.SetBasicAuth(credential.Username, credential.Secret)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if responseHeaders != nil {
		for key, values := range response.Header {
			responseHeaders[key] = append([]string(nil), values...)
		}
	}
	if response.StatusCode != http.StatusOK {
		message := "the registry rejected the image request"
		status := http.StatusBadGateway
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			status = http.StatusForbidden
			message = "the image is private or the repository is not accessible"
		case http.StatusNotFound:
			status = http.StatusNotFound
			message = "the registry repository or tag was not found"
		case http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
			message = "registry rate limit reached; retry later or configure registry credentials"
		}
		return &registryStatusError{Status: status, Message: message}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, limit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}
