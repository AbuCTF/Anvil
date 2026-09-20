package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
)

// Service handles container lifecycle management
type Service struct {
	config config.ContainerConfig
	client *client.Client
	logger *zap.Logger

	networkID string
}

const interContainerCommunicationOption = "com.docker.network.bridge.enable_icc"

func NewService(cfg config.ContainerConfig, logger *zap.Logger) (*Service, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Service{
		config: cfg,
		client: cli,
		logger: logger,
	}

	// docker is optional: on kubernetes (no docker.sock) the platform still runs;
	// container-challenge ops degrade to errors until the k8s instancer handles them.
	if _, err = cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		logger.Warn("Docker not available; container-challenge features disabled", zap.Error(err))
		return s, nil
	}

	if err := s.ensureNetwork(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure network: %w", err)
	}

	go s.cleanupLoop()

	return s, nil
}

func (s *Service) Status() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := s.client.Ping(ctx, client.PingOptions{})
	if err != nil {
		return "disconnected"
	}
	return "connected"
}

func (s *Service) ensureNetwork(ctx context.Context) error {
	networks, err := s.client.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("name", s.config.NetworkName),
	})
	if err != nil {
		return err
	}

	if len(networks.Items) > 0 {
		s.networkID = networks.Items[0].ID
		if networks.Items[0].Options[interContainerCommunicationOption] != "false" {
			s.logger.Warn("Existing challenge network permits inter-container communication; recreate it to apply isolation",
				zap.String("network", s.config.NetworkName),
			)
		}
		s.logger.Info("Using existing network", zap.String("network", s.config.NetworkName))
		return nil
	}

	createOptions, err := challengeNetworkCreateOptions(s.config)
	if err != nil {
		return err
	}
	resp, err := s.client.NetworkCreate(ctx, s.config.NetworkName, createOptions)
	if err != nil {
		return err
	}

	s.networkID = resp.ID
	s.logger.Info("Created network", zap.String("network", s.config.NetworkName), zap.String("id", resp.ID))
	return nil
}

func challengeNetworkCreateOptions(cfg config.ContainerConfig) (client.NetworkCreateOptions, error) {
	subnet, err := netip.ParsePrefix(cfg.NetworkSubnet)
	if err != nil {
		return client.NetworkCreateOptions{}, fmt.Errorf("invalid container network subnet %q: %w", cfg.NetworkSubnet, err)
	}
	return client.NetworkCreateOptions{
		Driver: "bridge",
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{
				{
					Subnet: subnet,
				},
			},
		},
		Options: map[string]string{
			interContainerCommunicationOption: "false",
		},
		Labels: cfg.Labels,
	}, nil
}

type CreateInstanceRequest struct {
	InstanceID      uuid.UUID
	ChallengeSlug   string
	Image           string
	Tag             string
	Registry        string
	Platform        string // e.g. "linux/amd64" for cross-arch emulation
	ExposedPorts    []ExposedPort
	CPULimit        string
	MemoryLimit     string
	Labels          map[string]string
	EnvironmentVars []string
}

type ExposedPort struct {
	Port     int
	Protocol string
}

type CreateInstanceResponse struct {
	ContainerID   string
	ContainerName string
	IPAddress     string
}

func (s *Service) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*CreateInstanceResponse, error) {
	image := req.Image
	if req.Registry != "" {
		image = req.Registry + "/" + image
	}
	if !strings.Contains(image, ":") {
		if req.Tag != "" {
			image = image + ":" + req.Tag
		} else {
			image = image + ":latest"
		}
	}

	if err := s.pullImage(ctx, image, req.Platform); err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	exposedPorts := make(network.PortSet)
	for _, p := range req.ExposedPorts {
		protocol := p.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		containerPort, err := network.ParsePort(fmt.Sprintf("%d/%s", p.Port, protocol))
		if err != nil {
			return nil, fmt.Errorf("invalid exposed port: %w", err)
		}
		exposedPorts[containerPort] = struct{}{}
	}

	containerName := fmt.Sprintf("anvil-%s-%s", req.ChallengeSlug, req.InstanceID.String()[:8])

	labels := make(map[string]string)
	for k, v := range s.config.Labels {
		labels[k] = v
	}
	for k, v := range req.Labels {
		labels[k] = v
	}
	labels["anvil.instance.id"] = req.InstanceID.String()
	labels["anvil.challenge.slug"] = req.ChallengeSlug

	cpuLimit, _ := parseCPULimit(req.CPULimit)
	memoryLimit, _ := parseMemoryLimit(req.MemoryLimit)

	// Build platform spec for cross-arch emulation (e.g. amd64 image on arm64 host)
	var platform *ocispec.Platform
	if req.Platform != "" {
		parts := strings.SplitN(req.Platform, "/", 3)
		if len(parts) >= 2 {
			platform = &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
			if len(parts) == 3 {
				platform.Variant = parts[2]
			}
		}
	}

	containerCfg := &container.Config{
		Image:        image,
		ExposedPorts: exposedPorts,
		Labels:       labels,
		Env:          req.EnvironmentVars,
	}
	hostCfg := &container.HostConfig{
		NetworkMode:  container.NetworkMode(s.config.NetworkName),
		PortBindings: network.PortMap{},
		Resources: container.Resources{
			NanoCPUs: cpuLimit,
			Memory:   memoryLimit,
		},
		RestartPolicy: container.RestartPolicy{
			Name:              "on-failure",
			MaximumRetryCount: 3,
		},
	}

	createOptions := client.ContainerCreateOptions{
		Config:     containerCfg,
		HostConfig: hostCfg,
		Platform:   platform,
		Name:       containerName,
	}
	resp, err := s.client.ContainerCreate(ctx, createOptions)
	if err != nil && platform != nil && strings.Contains(err.Error(), "does not provide the specified platform") {
		// The local image does not have the requested platform variant (e.g. the
		// image is amd64-only but arm64 was requested).  Retry without a platform
		// constraint so the daemon runs the image with whatever arch is available,
		// using QEMU/binfmt emulation when necessary.
		s.logger.Warn("ContainerCreate with platform spec failed; retrying without platform constraint",
			zap.String("image", image), zap.String("platform", req.Platform), zap.Error(err))
		createOptions.Platform = nil
		resp, err = s.client.ContainerCreate(ctx, createOptions)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if _, err := s.client.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = s.client.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Get container IP — inspect after start to allow Docker to assign the address
	ipAddress := ""
	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		inspectResult, err := s.client.ContainerInspect(ctx, resp.ID, client.ContainerInspectOptions{})
		if err != nil {
			s.logger.Warn("Failed to inspect container", zap.Error(err))
			break
		}
		inspect := inspectResult.Container
		if inspect.NetworkSettings != nil {
			// Log all networks on first attempt for debugging
			if i == 0 {
				netNames := make([]string, 0)
				for k, v := range inspect.NetworkSettings.Networks {
					netNames = append(netNames, fmt.Sprintf("%s=%s", k, v.IPAddress.String()))
				}
				s.logger.Info("Container inspect networks",
					zap.String("container", resp.ID[:12]),
					zap.Strings("network_ips", netNames),
				)
			}
			if net, ok := inspect.NetworkSettings.Networks[s.config.NetworkName]; ok && net.IPAddress.IsValid() {
				ipAddress = net.IPAddress.String()
				break
			}
			// Fallback: grab any available IP
			for _, net := range inspect.NetworkSettings.Networks {
				if net.IPAddress.IsValid() {
					ipAddress = net.IPAddress.String()
					break
				}
			}
			if ipAddress != "" {
				break
			}
		}
	}

	s.logger.Info("Created container",
		zap.String("container_id", resp.ID[:12]),
		zap.String("name", containerName),
		zap.String("ip", ipAddress),
		zap.String("network", s.config.NetworkName),
	)

	return &CreateInstanceResponse{
		ContainerID:   resp.ID,
		ContainerName: containerName,
		IPAddress:     ipAddress,
	}, nil
}

// StopInstance stops and removes a container.
func (s *Service) StopInstance(ctx context.Context, containerID string) error {
	timeout := 10 // seconds
	if _, err := s.client.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		s.logger.Warn("ContainerStop returned error; force-removing anyway",
			zap.String("container", containerID), zap.Error(err))
	}
	_, err := s.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return err
}

func (s *Service) StartInstance(ctx context.Context, containerID string) error {
	_, err := s.client.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
	return err
}

func (s *Service) RemoveInstance(ctx context.Context, containerID string) error {
	_, err := s.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	return err
}

func (s *Service) GetInstanceStatus(ctx context.Context, containerID string) (string, error) {
	inspect, err := s.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return "", err
	}
	return string(inspect.Container.State.Status), nil
}

func (s *Service) GetInstanceLogs(ctx context.Context, containerID string, tail int) (string, error) {
	options := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	}

	logs, err := s.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", err
	}
	defer logs.Close()

	content, err := io.ReadAll(logs)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// ListInstances lists all Anvil-managed containers
func (s *Service) ListInstances(ctx context.Context) ([]container.Summary, error) {
	result, err := s.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", "managed-by=anvil"),
	})
	return result.Items, err
}

// Cleanup removes all expired or orphaned containers
func (s *Service) Cleanup(ctx context.Context) error {
	containers, err := s.ListInstances(ctx)
	if err != nil {
		return err
	}

	for _, c := range containers {
		// This will be coordinated with the database
		s.logger.Debug("Cleanup check", zap.String("container", c.ID[:12]))
	}

	return nil
}

func (s *Service) cleanupLoop() {
	ticker := time.NewTicker(s.config.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := s.Cleanup(ctx); err != nil {
			s.logger.Error("Cleanup failed", zap.Error(err))
		}
		cancel()
	}
}

func (s *Service) pullImage(ctx context.Context, image string, platform string) error {
	imageExists := false
	_, err := s.client.ImageInspect(ctx, image)
	if err == nil {
		imageExists = true
		// If a specific platform is requested, always re-pull to ensure correct arch
		if platform == "" {
			return nil // Image exists, no platform constraint
		}
	}

	s.logger.Info("Pulling image", zap.String("image", image), zap.String("platform", platform))

	authStr := getRegistryAuth(image)
	pullOpts := client.ImagePullOptions{}
	if parsed := parsePlatform(platform); parsed != nil {
		pullOpts.Platforms = []ocispec.Platform{*parsed}
	}
	if authStr != "" {
		pullOpts.RegistryAuth = authStr
	}

	reader, err := s.client.ImagePull(ctx, image, pullOpts)
	if err != nil {
		if platform != "" {
			if imageExists {
				// A local copy already exists (possibly a different arch); use it and
				// let ContainerCreate decide whether it is compatible.
				s.logger.Warn("Platform-specific image pull failed; using existing local image",
					zap.String("image", image), zap.String("platform", platform), zap.Error(err))
				return nil
			}
			// No local copy — retry without a platform constraint so we pull whatever
			// the registry offers (the daemon will use QEMU/binfmt emulation if needed).
			s.logger.Warn("Platform-specific image pull failed; retrying without platform constraint",
				zap.String("image", image), zap.String("platform", platform), zap.Error(err))
			fallbackOpts := client.ImagePullOptions{}
			if authStr != "" {
				fallbackOpts.RegistryAuth = authStr
			}
			reader, err = s.client.ImagePull(ctx, image, fallbackOpts)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	defer reader.Close()

	// Wait for pull to complete
	_, err = io.Copy(io.Discard, reader)
	return err
}

func parsePlatform(value string) *ocispec.Platform {
	if value == "" {
		return nil
	}
	parts := strings.SplitN(value, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	platform := &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		platform.Variant = parts[2]
	}
	return platform
}

// getRegistryAuth reads auth from ~/.docker/config.json for the image's registry
func getRegistryAuth(image string) string {
	configFile := "/root/.docker/config.json"
	data, err := os.ReadFile(configFile)
	if err != nil {
		return ""
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}

	// Extract registry from image reference (e.g. "ghcr.io/user/repo:tag" → "ghcr.io")
	registry := "docker.io"
	parts := strings.SplitN(image, "/", 2)
	if len(parts) > 1 && strings.Contains(parts[0], ".") {
		registry = parts[0]
	}

	auth, ok := cfg.Auths[registry]
	if !ok || auth.Auth == "" {
		// Try with https:// prefix
		auth, ok = cfg.Auths["https://"+registry]
		if !ok || auth.Auth == "" {
			return ""
		}
	}

	// Docker API expects base64-encoded JSON with username and password.
	// The config.json "auth" field is base64(user:pass) — decode and split.
	decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
	if err != nil {
		return ""
	}
	parts2 := strings.SplitN(string(decoded), ":", 2)
	if len(parts2) != 2 {
		return ""
	}
	authJSON, _ := json.Marshal(map[string]string{
		"username":      parts2[0],
		"password":      parts2[1],
		"serveraddress": registry,
	})
	return base64.StdEncoding.EncodeToString(authJSON)
}

// parseCPULimit parses CPU limit string to nanocpus
func parseCPULimit(limit string) (int64, error) {
	if limit == "" {
		return 0, nil
	}
	var cpus float64
	_, err := fmt.Sscanf(limit, "%f", &cpus)
	if err != nil {
		return 0, err
	}
	return int64(cpus * 1e9), nil
}

// parseMemoryLimit parses memory limit string to bytes
func parseMemoryLimit(limit string) (int64, error) {
	if limit == "" {
		return 0, nil
	}

	limit = strings.ToLower(limit)
	var value int64
	var unit string

	_, err := fmt.Sscanf(limit, "%d%s", &value, &unit)
	if err != nil {
		return 0, err
	}

	switch unit {
	case "k", "kb":
		return value * 1024, nil
	case "m", "mb":
		return value * 1024 * 1024, nil
	case "g", "gb":
		return value * 1024 * 1024 * 1024, nil
	default:
		return value, nil
	}
}

// HealthCheck checks container health
func (s *Service) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	inspect, err := s.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return false, err
	}

	return inspect.Container.State.Running, nil
}

// ExecInContainer executes a command in a container (for health checks)
func (s *Service) ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, error) {
	execConfig := client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := s.client.ExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", err
	}

	resp, err := s.client.ExecAttach(ctx, execID.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	output, err := io.ReadAll(resp.Reader)
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// GetNetworkInfo returns network information for VPN routing
func (s *Service) GetNetworkInfo() (string, string) {
	return s.networkID, s.config.NetworkSubnet
}

type ContainerStats struct {
	TotalContainers   int
	RunningContainers int
	StoppedContainers int
}

func (s *Service) Stats(ctx context.Context) (*ContainerStats, error) {
	containers, err := s.ListInstances(ctx)
	if err != nil {
		return nil, err
	}

	stats := &ContainerStats{
		TotalContainers: len(containers),
	}

	for _, c := range containers {
		if c.State == "running" {
			stats.RunningContainers++
		} else {
			stats.StoppedContainers++
		}
	}

	return stats, nil
}
