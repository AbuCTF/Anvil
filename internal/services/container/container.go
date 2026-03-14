package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
)

// Service handles container lifecycle management
type Service struct {
	config config.ContainerConfig
	client *client.Client
	logger *zap.Logger

	// Network management
	networkID string

	// Host port pool — only used when HostPortMin > 0
	mu            sync.Mutex
	usedHostPorts map[int]bool
}

// NewService creates a new container service
func NewService(cfg config.ContainerConfig, logger *zap.Logger) (*Service, error) {
	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	s := &Service{
		config:        cfg,
		client:        cli,
		logger:        logger,
		usedHostPorts: make(map[int]bool),
	}

	// Ensure network exists
	if err := s.ensureNetwork(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure network: %w", err)
	}

	// Rebuild in-memory host port pool from any containers already running
	s.loadUsedHostPorts(context.Background())

	// Start cleanup goroutine
	go s.cleanupLoop()

	return s, nil
}

// Status returns the service status
func (s *Service) Status() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := s.client.Ping(ctx)
	if err != nil {
		return "disconnected"
	}
	return "connected"
}

// ensureNetwork creates the challenge network if it doesn't exist
func (s *Service) ensureNetwork(ctx context.Context) error {
	// Check if network exists
	networks, err := s.client.NetworkList(ctx, types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("name", s.config.NetworkName)),
	})
	if err != nil {
		return err
	}

	if len(networks) > 0 {
		s.networkID = networks[0].ID
		s.logger.Info("Using existing network", zap.String("network", s.config.NetworkName))
		return nil
	}

	// Create network
	resp, err := s.client.NetworkCreate(ctx, s.config.NetworkName, types.NetworkCreate{
		Driver: "bridge",
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{
				{
					Subnet: s.config.NetworkSubnet,
				},
			},
		},
		Labels: s.config.Labels,
	})
	if err != nil {
		return err
	}

	s.networkID = resp.ID
	s.logger.Info("Created network", zap.String("network", s.config.NetworkName), zap.String("id", resp.ID))
	return nil
}

// CreateInstanceRequest contains the request to create a container instance
type CreateInstanceRequest struct {
	InstanceID      uuid.UUID
	ChallengeSlug   string
	Image           string
	Tag             string
	Registry        string
	Platform        string // e.g. "linux/amd64" for cross-arch emulation
	ExposedPorts    []ExposedPort
	// HostPortBindings maps each ExposedPort to a pre-allocated host port.
	// Must be the same length as ExposedPorts when set.
	// Leave nil to use VPN-only direct-container-IP access (no -p binding).
	HostPortBindings []HostPortBinding
	CPULimit        string
	MemoryLimit     string
	Labels          map[string]string
	EnvironmentVars []string
}

// ExposedPort represents a port to expose
type ExposedPort struct {
	Port     int
	Protocol string
}

// HostPortBinding maps a container port to a specific host port.
type HostPortBinding struct {
	ContainerPort int
	HostPort      int
	Protocol      string // "tcp" or "udp"; defaults to "tcp"
}

// CreateInstanceResponse contains the response from creating a container
type CreateInstanceResponse struct {
	ContainerID   string
	ContainerName string
	IPAddress     string
	// HostPorts maps container port to the allocated host port (only set when
	// HostPortBindings were requested).
	HostPorts map[int]int
}

// CreateInstance creates a new challenge container
func (s *Service) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*CreateInstanceResponse, error) {
	// Build image name
	image := req.Image
	if req.Registry != "" {
		image = req.Registry + "/" + image
	}
	// Only append tag if image doesn't already contain one
	if !strings.Contains(image, ":") {
		if req.Tag != "" {
			image = image + ":" + req.Tag
		} else {
			image = image + ":latest"
		}
	}

	// Pull image if needed (with platform for cross-arch support)
	if err := s.pullImage(ctx, image, req.Platform); err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	// Build exposed ports set and optional host port bindings
	exposedPorts := make(nat.PortSet)
	portBindings := nat.PortMap{}
	for i, p := range req.ExposedPorts {
		protocol := p.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		containerPort := nat.Port(fmt.Sprintf("%d/%s", p.Port, protocol))
		exposedPorts[containerPort] = struct{}{}

		if i < len(req.HostPortBindings) {
			hpb := req.HostPortBindings[i]
			hProto := hpb.Protocol
			if hProto == "" {
				hProto = "tcp"
			}
			hostContainerPort := nat.Port(fmt.Sprintf("%d/%s", hpb.ContainerPort, hProto))
			portBindings[hostContainerPort] = []nat.PortBinding{
				{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hpb.HostPort)},
			}
		}
	}

	// Container name
	containerName := fmt.Sprintf("anvil-%s-%s", req.ChallengeSlug, req.InstanceID.String()[:8])

	// Add default labels
	labels := make(map[string]string)
	for k, v := range s.config.Labels {
		labels[k] = v
	}
	for k, v := range req.Labels {
		labels[k] = v
	}
	labels["anvil.instance.id"] = req.InstanceID.String()
	labels["anvil.challenge.slug"] = req.ChallengeSlug

	// Parse resource limits
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
		PortBindings: portBindings,
		Resources: container.Resources{
			NanoCPUs: cpuLimit,
			Memory:   memoryLimit,
		},
		RestartPolicy: container.RestartPolicy{
			Name:              "on-failure",
			MaximumRetryCount: 3,
		},
	}

	// Create container directly on the challenge network
	resp, err := s.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, platform, containerName)
	if err != nil && platform != nil && strings.Contains(err.Error(), "does not provide the specified platform") {
		// The local image does not have the requested platform variant (e.g. the
		// image is amd64-only but arm64 was requested).  Retry without a platform
		// constraint so the daemon runs the image with whatever arch is available,
		// using QEMU/binfmt emulation when necessary.
		s.logger.Warn("ContainerCreate with platform spec failed; retrying without platform constraint",
			zap.String("image", image), zap.String("platform", req.Platform), zap.Error(err))
		resp, err = s.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, containerName)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := s.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		s.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Get container IP — inspect after start to allow Docker to assign the address
	ipAddress := ""
	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		inspect, err := s.client.ContainerInspect(ctx, resp.ID)
		if err != nil {
			s.logger.Warn("Failed to inspect container", zap.Error(err))
			break
		}
		if inspect.NetworkSettings != nil {
			// Log all networks on first attempt for debugging
			if i == 0 {
				netNames := make([]string, 0)
				for k, v := range inspect.NetworkSettings.Networks {
					netNames = append(netNames, fmt.Sprintf("%s=%s", k, v.IPAddress))
				}
				s.logger.Info("Container inspect networks",
					zap.String("container", resp.ID[:12]),
					zap.Strings("network_ips", netNames),
					zap.String("global_ip", inspect.NetworkSettings.IPAddress),
				)
			}
			// Try exact network name match
			if net, ok := inspect.NetworkSettings.Networks[s.config.NetworkName]; ok && net.IPAddress != "" {
				ipAddress = net.IPAddress
				break
			}
			// Fallback: grab any available IP
			for _, net := range inspect.NetworkSettings.Networks {
				if net.IPAddress != "" {
					ipAddress = net.IPAddress
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

	// Build host port map for the response
	var hostPorts map[int]int
	if len(req.HostPortBindings) > 0 {
		hostPorts = make(map[int]int, len(req.HostPortBindings))
		for _, hpb := range req.HostPortBindings {
			hostPorts[hpb.ContainerPort] = hpb.HostPort
		}
	}

	return &CreateInstanceResponse{
		ContainerID:   resp.ID,
		ContainerName: containerName,
		IPAddress:     ipAddress,
		HostPorts:     hostPorts,
	}, nil
}

// StopInstance stops and removes a container, freeing any host port bindings.
func (s *Service) StopInstance(ctx context.Context, containerID string) error {
	timeout := 10 // seconds
	if err := s.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		// Best-effort: proceed to force-remove even if stop fails
		s.logger.Warn("ContainerStop returned error; force-removing anyway",
			zap.String("container", containerID), zap.Error(err))
	}
	// Remove the container so that host port bindings are released immediately
	return s.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
}

// StartInstance starts a stopped container
func (s *Service) StartInstance(ctx context.Context, containerID string) error {
	return s.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

// RemoveInstance removes a container
func (s *Service) RemoveInstance(ctx context.Context, containerID string) error {
	return s.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
}

// GetInstanceStatus gets the status of a container
func (s *Service) GetInstanceStatus(ctx context.Context, containerID string) (string, error) {
	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	return inspect.State.Status, nil
}

// GetInstanceLogs gets the logs from a container
func (s *Service) GetInstanceLogs(ctx context.Context, containerID string, tail int) (string, error) {
	options := container.LogsOptions{
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
func (s *Service) ListInstances(ctx context.Context) ([]types.Container, error) {
	return s.client.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "managed-by=anvil"),
		),
	})
}

// Cleanup removes all expired or orphaned containers
func (s *Service) Cleanup(ctx context.Context) error {
	containers, err := s.ListInstances(ctx)
	if err != nil {
		return err
	}

	for _, c := range containers {
		// Check if container should be cleaned up
		// This will be coordinated with the database
		s.logger.Debug("Cleanup check", zap.String("container", c.ID[:12]))
	}

	return nil
}

// cleanupLoop runs periodic cleanup
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

// ============================================================================
// Host port pool — used for -p host_port:container_port binding mode
// ============================================================================

// AllocateHostPort picks a free port from the configured range and marks it as
// in-use. Returns an error when the pool is exhausted or is not configured.
func (s *Service) AllocateHostPort() (int, error) {
	if s.config.HostPortMin <= 0 || s.config.HostPortMax <= 0 {
		return 0, fmt.Errorf("host port pool is not configured (set host_port_min / host_port_max)")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for port := s.config.HostPortMin; port <= s.config.HostPortMax; port++ {
		if !s.usedHostPorts[port] {
			s.usedHostPorts[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("host port pool exhausted (range %d-%d)", s.config.HostPortMin, s.config.HostPortMax)
}

// ReleaseHostPort returns a port to the pool so it can be reused.
func (s *Service) ReleaseHostPort(port int) {
	s.mu.Lock()
	delete(s.usedHostPorts, port)
	s.mu.Unlock()
}

// HostPortMappingEnabled returns true when the host port pool is configured.
func (s *Service) HostPortMappingEnabled() bool {
	return s.config.HostPortMin > 0 && s.config.HostPortMax > 0
}

// loadUsedHostPorts scans currently running Anvil containers and marks their
// host port bindings as in-use so we never double-allocate on restart.
func (s *Service) loadUsedHostPorts(ctx context.Context) {
	if !s.HostPortMappingEnabled() {
		return
	}

	containers, err := s.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "managed-by=anvil")),
	})
	if err != nil {
		s.logger.Warn("failed to list containers while initialising host port pool", zap.Error(err))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range containers {
		for _, pm := range c.Ports {
			if pm.PublicPort > 0 {
				s.usedHostPorts[int(pm.PublicPort)] = true
			}
		}
	}

	s.logger.Info("host port pool initialised",
		zap.Int("in_use", len(s.usedHostPorts)),
		zap.Int("range_start", s.config.HostPortMin),
		zap.Int("range_end", s.config.HostPortMax),
	)
}

// pullImage pulls a Docker image
func (s *Service) pullImage(ctx context.Context, image string, platform string) error {
	// Check if image exists locally
	imageExists := false
	_, _, err := s.client.ImageInspectWithRaw(ctx, image)
	if err == nil {
		imageExists = true
		// If a specific platform is requested, always re-pull to ensure correct arch
		if platform == "" {
			return nil // Image exists, no platform constraint
		}
	}

	s.logger.Info("Pulling image", zap.String("image", image), zap.String("platform", platform))

	// Try to load registry auth from Docker config
	authStr := getRegistryAuth(image)
	pullOpts := types.ImagePullOptions{
		Platform: platform, // e.g. "linux/amd64" — empty string means native arch
	}
	if authStr != "" {
		pullOpts.RegistryAuth = authStr
	}

	reader, err := s.client.ImagePull(ctx, image, pullOpts)
	if err != nil {
		if platform != "" {
			// Platform-specific pull failed.
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
			fallbackOpts := types.ImagePullOptions{}
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
	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, err
	}

	return inspect.State.Running, nil
}

// ExecInContainer executes a command in a container (for health checks)
func (s *Service) ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, error) {
	execConfig := types.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := s.client.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", err
	}

	resp, err := s.client.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
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

// Stats returns container statistics
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
