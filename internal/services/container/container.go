package container

import (
	"context"
	"fmt"
	"io"
	"net"
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
	"go.uber.org/zap"
)

// Service handles container lifecycle management
type Service struct {
	config  config.ContainerConfig
	client  *client.Client
	logger  *zap.Logger
	
	// Port allocation
	portMu    sync.Mutex
	usedPorts map[int]bool
	portRange [2]int // [start, end]
	
	// Network management
	networkID string
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
		config:    cfg,
		client:    cli,
		logger:    logger,
		usedPorts: make(map[int]bool),
		portRange: [2]int{32000, 33000}, // Dynamic port range
	}

	// Ensure network exists
	if err := s.ensureNetwork(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure network: %w", err)
	}

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
	ExposedPorts    []ExposedPort
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

// CreateInstanceResponse contains the response from creating a container
type CreateInstanceResponse struct {
	ContainerID   string
	ContainerName string
	IPAddress     string
	PortMappings  map[string]int // {"80/tcp": 32001}
}

// CreateInstance creates a new challenge container
func (s *Service) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*CreateInstanceResponse, error) {
	// Build image name
	image := req.Image
	if req.Registry != "" {
		image = req.Registry + "/" + image
	}
	if req.Tag != "" {
		image = image + ":" + req.Tag
	} else {
		image = image + ":latest"
	}

	// Pull image if needed
	if err := s.pullImage(ctx, image); err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	// Allocate ports
	portMappings, portBindings, exposedPorts := s.allocatePorts(req.ExposedPorts)

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

	// Create container
	resp, err := s.client.ContainerCreate(ctx,
		&container.Config{
			Image:        image,
			ExposedPorts: exposedPorts,
			Labels:       labels,
			Env:          req.EnvironmentVars,
		},
		&container.HostConfig{
			PortBindings: portBindings,
			NetworkMode:  container.NetworkMode(s.config.NetworkName),
			Resources: container.Resources{
				NanoCPUs: cpuLimit,
				Memory:   memoryLimit,
			},
			RestartPolicy: container.RestartPolicy{
				Name:              "on-failure",
				MaximumRetryCount: 3,
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				s.config.NetworkName: {},
			},
		},
		nil,
		containerName,
	)
	if err != nil {
		s.releasePorts(portMappings)
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := s.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		s.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		s.releasePorts(portMappings)
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Get container IP
	inspect, err := s.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		s.logger.Warn("Failed to inspect container", zap.Error(err))
	}

	ipAddress := ""
	if inspect.NetworkSettings != nil {
		if net, ok := inspect.NetworkSettings.Networks[s.config.NetworkName]; ok {
			ipAddress = net.IPAddress
		}
	}

	s.logger.Info("Created container",
		zap.String("container_id", resp.ID[:12]),
		zap.String("name", containerName),
		zap.String("ip", ipAddress),
	)

	return &CreateInstanceResponse{
		ContainerID:   resp.ID,
		ContainerName: containerName,
		IPAddress:     ipAddress,
		PortMappings:  portMappings,
	}, nil
}

// StopInstance stops a container
func (s *Service) StopInstance(ctx context.Context, containerID string) error {
	timeout := 10 // seconds
	return s.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

// RemoveInstance removes a container
func (s *Service) RemoveInstance(ctx context.Context, containerID string) error {
	// Get container info to release ports
	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err == nil {
		// Release allocated ports
		for _, bindings := range inspect.HostConfig.PortBindings {
			for _, binding := range bindings {
				var port int
				fmt.Sscanf(binding.HostPort, "%d", &port)
				s.releasePort(port)
			}
		}
	}

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

// pullImage pulls a Docker image
func (s *Service) pullImage(ctx context.Context, image string) error {
	// Check if image exists locally
	_, _, err := s.client.ImageInspectWithRaw(ctx, image)
	if err == nil {
		return nil // Image exists
	}

	s.logger.Info("Pulling image", zap.String("image", image))

	reader, err := s.client.ImagePull(ctx, image, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	// Wait for pull to complete
	_, err = io.Copy(io.Discard, reader)
	return err
}

// allocatePorts allocates host ports for container ports
func (s *Service) allocatePorts(ports []ExposedPort) (map[string]int, nat.PortMap, nat.PortSet) {
	s.portMu.Lock()
	defer s.portMu.Unlock()

	portMappings := make(map[string]int)
	portBindings := make(nat.PortMap)
	exposedPorts := make(nat.PortSet)

	for _, p := range ports {
		protocol := p.Protocol
		if protocol == "" {
			protocol = "tcp"
		}

		// Find available port
		hostPort := s.findAvailablePort()
		if hostPort == 0 {
			continue // No ports available
		}

		containerPort := nat.Port(fmt.Sprintf("%d/%s", p.Port, protocol))
		portKey := fmt.Sprintf("%d/%s", p.Port, protocol)

		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", hostPort),
			},
		}
		portMappings[portKey] = hostPort
		s.usedPorts[hostPort] = true
	}

	return portMappings, portBindings, exposedPorts
}

// findAvailablePort finds an available port in the range
func (s *Service) findAvailablePort() int {
	for port := s.portRange[0]; port <= s.portRange[1]; port++ {
		if !s.usedPorts[port] && s.isPortAvailable(port) {
			return port
		}
	}
	return 0
}

// isPortAvailable checks if a port is available on the host
func (s *Service) isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// releasePorts releases allocated ports
func (s *Service) releasePorts(portMappings map[string]int) {
	s.portMu.Lock()
	defer s.portMu.Unlock()

	for _, port := range portMappings {
		delete(s.usedPorts, port)
	}
}

// releasePort releases a single port
func (s *Service) releasePort(port int) {
	s.portMu.Lock()
	defer s.portMu.Unlock()
	delete(s.usedPorts, port)
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
	TotalContainers  int
	RunningContainers int
	StoppedContainers int
	UsedPorts        int
	AvailablePorts   int
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

	s.portMu.Lock()
	stats.UsedPorts = len(s.usedPorts)
	stats.AvailablePorts = s.portRange[1] - s.portRange[0] - len(s.usedPorts)
	s.portMu.Unlock()

	return stats, nil
}
