package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
)

// Service handles container lifecycle management
type Service struct {
	config config.ContainerConfig
	client *client.Client
	logger *zap.Logger

	networkID    string
	portMu       sync.Mutex
	registryAuth func(context.Context, string) string
}

func (s *Service) SetRegistryAuthResolver(resolver func(context.Context, string) string) {
	s.registryAuth = resolver
}

func (s *Service) registryAuthFor(ctx context.Context, image string) string {
	if s.registryAuth != nil {
		if encoded := s.registryAuth(ctx, image); encoded != "" {
			return encoded
		}
	}
	return getRegistryAuth(image)
}

// SwarmNodeInfo is the manager's live view of a Docker runtime node. It is
// deliberately separate from vm_nodes so container capacity can never be
// selected accidentally by the libvirt scheduler.
type SwarmNodeInfo struct {
	ID               string
	Name             string
	Hostname         string
	IPAddress        string
	Status           string
	Availability     string
	Architecture     string
	Manager          bool
	Leader           bool
	TotalVCPU        int
	UsedVCPU         int
	TotalMemoryMB    int
	UsedMemoryMB     int
	TotalDiskGB      int
	ActiveWorkloads  int
	MaximumWorkloads int
	CreatedAt        time.Time
}

const interContainerCommunicationOption = "com.docker.network.bridge.enable_icc"

const (
	// TCPRoutesLabel tells the single-host TCP router which public pool ports
	// forward to which container ports. The value is a JSON object whose keys are
	// public ports and whose values are container ports.
	TCPRoutesLabel = "anvil.tcp.routes"
	// TCPNetworkLabel identifies the isolated Docker network containing the
	// challenge backend.
	TCPNetworkLabel = "anvil.tcp.network"
	// TCPRouterComponentLabel identifies the trusted host-network router.
	TCPRouterComponentLabel = "anvil.component=tcp-router"
)

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

	if s.usingSwarm() {
		if err := s.ensureSwarmManager(ctx); err != nil {
			return nil, err
		}
		if cfg.HTTPRouting {
			if err := os.MkdirAll(cfg.HTTPRoutesPath, 0o750); err != nil {
				return nil, fmt.Errorf("create Swarm HTTP routes directory: %w", err)
			}
		}
	} else if err := s.ensureNetwork(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure network: %w", err)
	}

	go s.cleanupLoop()

	return s, nil
}

func (s *Service) usingSwarm() bool {
	return strings.EqualFold(strings.TrimSpace(s.config.Orchestrator), "swarm")
}

func (s *Service) ensureSwarmManager(ctx context.Context) error {
	info, err := s.client.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("inspect Docker Swarm: %w", err)
	}
	if info.Info.Swarm.LocalNodeState != swarm.LocalNodeStateActive || !info.Info.Swarm.ControlAvailable {
		return fmt.Errorf("container orchestrator is swarm but this Docker daemon is not an active manager")
	}
	return nil
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

// SwarmNodes returns Docker runtime capacity directly from the Swarm manager.
// A standalone Docker daemon has no runtime nodes and is not an error.
func (s *Service) SwarmNodes(ctx context.Context) ([]SwarmNodeInfo, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}
	info, err := s.client.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect Docker runtime: %w", err)
	}
	if info.Info.Swarm.LocalNodeState != swarm.LocalNodeStateActive || !info.Info.Swarm.ControlAvailable {
		return nil, nil
	}

	nodeResult, err := s.client.NodeList(ctx, client.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Swarm nodes: %w", err)
	}
	taskResult, err := s.client.TaskList(ctx, client.TaskListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Swarm tasks: %w", err)
	}

	type usage struct {
		nanoCPU int64
		memory  int64
		active  int
	}
	byNode := make(map[string]usage)
	for _, task := range taskResult.Items {
		if task.NodeID == "" || task.DesiredState != swarm.TaskStateRunning {
			continue
		}
		current := byNode[task.NodeID]
		current.active++
		if task.Spec.Resources != nil && task.Spec.Resources.Reservations != nil {
			current.nanoCPU += task.Spec.Resources.Reservations.NanoCPUs
			current.memory += task.Spec.Resources.Reservations.MemoryBytes
		}
		byNode[task.NodeID] = current
	}

	nodes := make([]SwarmNodeInfo, 0, len(nodeResult.Items))
	for _, node := range nodeResult.Items {
		if node.Spec.Labels["anvil.runtime"] != "docker" {
			continue
		}
		u := byNode[node.ID]
		name := strings.TrimSpace(node.Spec.Labels["anvil.node"])
		if name == "" {
			name = node.Description.Hostname
		}
		diskGB, _ := strconv.Atoi(node.Spec.Labels["anvil.disk_gb"])
		maxWorkloads, _ := strconv.Atoi(node.Spec.Labels["anvil.max_workloads"])
		if maxWorkloads < 1 {
			maxWorkloads = max(1, int(node.Description.Resources.MemoryBytes/(512*1024*1024)))
		}
		nodes = append(nodes, SwarmNodeInfo{
			ID:               node.ID,
			Name:             name,
			Hostname:         node.Description.Hostname,
			IPAddress:        node.Status.Addr,
			Status:           string(node.Status.State),
			Availability:     string(node.Spec.Availability),
			Architecture:     node.Description.Platform.Architecture,
			Manager:          node.ManagerStatus != nil,
			Leader:           node.ManagerStatus != nil && node.ManagerStatus.Leader,
			TotalVCPU:        int(node.Description.Resources.NanoCPUs / 1_000_000_000),
			UsedVCPU:         int((u.nanoCPU + 999_999_999) / 1_000_000_000),
			TotalMemoryMB:    int(node.Description.Resources.MemoryBytes / (1024 * 1024)),
			UsedMemoryMB:     int((u.memory + 1024*1024 - 1) / (1024 * 1024)),
			TotalDiskGB:      diskGB,
			ActiveWorkloads:  u.active,
			MaximumWorkloads: maxWorkloads,
			CreatedAt:        node.CreatedAt,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Leader != nodes[j].Leader {
			return nodes[i].Leader
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes, nil
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
		Driver:   "bridge",
		Internal: cfg.NetworkInternal,
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
	PublicHost      string
}

type ExposedPort struct {
	Port     int
	Protocol string
	Service  string
}

type CreateInstanceResponse struct {
	ContainerID    string
	ContainerName  string
	IPAddress      string
	PublicHost     string
	PublishedPorts map[string]int
}

var dnsLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func validHostname(host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func transportProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "tcp", "http", "https":
		return "tcp"
	case "udp":
		return "udp"
	case "sctp":
		return "sctp"
	default:
		return protocol
	}
}

func isHTTPPort(port ExposedPort) bool {
	service := strings.ToLower(strings.TrimSpace(port.Service))
	protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
	return service == "http" || service == "https" || protocol == "http" || protocol == "https"
}

func isRawTCPPort(port ExposedPort) bool {
	return !isHTTPPort(port) && transportProtocol(port.Protocol) == "tcp"
}

func hasHTTPExposedPort(ports []ExposedPort) bool {
	for _, port := range ports {
		if isHTTPPort(port) {
			return true
		}
	}
	return false
}

func nextAvailableTCPPort(minPort, maxPort int, used map[int]struct{}, available func(int) bool) (int, error) {
	for port := minPort; port <= maxPort; port++ {
		if _, exists := used[port]; exists {
			continue
		}
		if available != nil && !available(port) {
			continue
		}
		used[port] = struct{}{}
		return port, nil
	}
	return 0, fmt.Errorf("TCP instance port pool %d-%d is exhausted", minPort, maxPort)
}

func (s *Service) usedDockerTCPPorts(ctx context.Context) (map[int]struct{}, error) {
	result, err := s.client.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	used := make(map[int]struct{})
	for _, item := range result.Items {
		switch item.State {
		case "created", "running", "paused", "restarting":
		default:
			continue
		}
		for _, port := range item.Ports {
			if port.Type == "tcp" && port.PublicPort != 0 {
				used[int(port.PublicPort)] = struct{}{}
			}
		}
		if raw := item.Labels[TCPRoutesLabel]; raw != "" {
			var routes map[string]int
			if err := json.Unmarshal([]byte(raw), &routes); err != nil {
				return nil, fmt.Errorf("decode TCP routes on container %s: %w", item.ID, err)
			}
			for value := range routes {
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return nil, fmt.Errorf("invalid public TCP route %q on container %s", value, item.ID)
				}
				used[port] = struct{}{}
			}
		}
	}
	return used, nil
}

func (s *Service) tcpRouterReady(ctx context.Context) error {
	result, err := s.client.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("label", TCPRouterComponentLabel),
	})
	if err != nil {
		return fmt.Errorf("find Docker TCP router: %w", err)
	}
	for _, item := range result.Items {
		if item.State != "running" {
			continue
		}
		if item.Status == "" || strings.Contains(item.Status, "(healthy)") {
			return nil
		}
	}
	return fmt.Errorf("Docker TCP router is not running and healthy")
}

func httpRoutingLabels(routerName, host string, ports []ExposedPort) (map[string]string, error) {
	if !validHostname(host) {
		return nil, fmt.Errorf("invalid HTTP route hostname %q", host)
	}
	var target *ExposedPort
	for i := range ports {
		if isHTTPPort(ports[i]) {
			target = &ports[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("HTTP routing requested for a challenge without an HTTP port")
	}
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("invalid HTTP target port %d", target.Port)
	}

	labels := map[string]string{
		"traefik.enable": "true",
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", routerName):               "web",
		fmt.Sprintf("traefik.http.routers.%s.rule", routerName):                      fmt.Sprintf("Host(`%s`)", host),
		fmt.Sprintf("traefik.http.routers.%s.service", routerName):                   routerName,
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName): fmt.Sprintf("%d", target.Port),
	}
	if strings.EqualFold(target.Service, "https") || strings.EqualFold(target.Protocol, "https") {
		labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.scheme", routerName)] = "https"
	}
	return labels, nil
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
	if s.usingSwarm() {
		return s.createSwarmInstance(ctx, req, image)
	}

	if err := s.pullImage(ctx, image, req.Platform); err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	exposedPorts := make(network.PortSet)
	for _, p := range req.ExposedPorts {
		protocol := transportProtocol(p.Protocol)
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
	if req.PublicHost != "" && !validHostname(req.PublicHost) {
		return nil, fmt.Errorf("invalid public hostname %q", req.PublicHost)
	}
	if req.PublicHost != "" && hasHTTPExposedPort(req.ExposedPorts) {
		routeLabels, err := httpRoutingLabels(containerName, req.PublicHost, req.ExposedPorts)
		if err != nil {
			return nil, err
		}
		for k, v := range routeLabels {
			labels[k] = v
		}
	}

	cpuLimit, err := parseCPULimit(req.CPULimit)
	if err != nil {
		return nil, fmt.Errorf("invalid CPU limit: %w", err)
	}
	memoryLimit, err := parseMemoryLimit(req.MemoryLimit)
	if err != nil {
		return nil, fmt.Errorf("invalid memory limit: %w", err)
	}

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
		LogConfig: container.LogConfig{
			Type: "json-file",
			Config: map[string]string{
				"max-size": "10m",
				"max-file": "3",
			},
		},
		Resources: container.Resources{
			NanoCPUs: cpuLimit,
			Memory:   memoryLimit,
		},
		RestartPolicy: container.RestartPolicy{
			Name:              "on-failure",
			MaximumRetryCount: 3,
		},
	}

	publishedPorts := make(map[string]int)
	routeTargets := make(map[string]int)
	hasPublicTCP := false
	for _, port := range req.ExposedPorts {
		if isRawTCPPort(port) {
			hasPublicTCP = hasPublicTCP || s.config.TCPRouting
		}
	}
	if hasPublicTCP {
		s.portMu.Lock()
		defer s.portMu.Unlock()

		if err := s.tcpRouterReady(ctx); err != nil {
			return nil, err
		}
		used, err := s.usedDockerTCPPorts(ctx)
		if err != nil {
			return nil, fmt.Errorf("list published Docker ports: %w", err)
		}
		for _, exposed := range req.ExposedPorts {
			if !isRawTCPPort(exposed) {
				continue
			}
			hostPort, err := nextAvailableTCPPort(
				s.config.TCPPortMin,
				s.config.TCPPortMax,
				used,
				nil,
			)
			if err != nil {
				return nil, err
			}
			// Docker intentionally does not activate published ports for an
			// internal-only bridge. Keep the challenge isolated and let the trusted
			// host-network router forward this port based on the labels below.
			publishedPorts[fmt.Sprintf("%d/tcp", exposed.Port)] = hostPort
			routeTargets[strconv.Itoa(hostPort)] = exposed.Port
		}
		routesJSON, err := json.Marshal(routeTargets)
		if err != nil {
			return nil, fmt.Errorf("encode TCP routes: %w", err)
		}
		labels[TCPRoutesLabel] = string(routesJSON)
		labels[TCPNetworkLabel] = s.config.NetworkName
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
		ContainerID:    resp.ID,
		ContainerName:  containerName,
		IPAddress:      ipAddress,
		PublicHost:     req.PublicHost,
		PublishedPorts: publishedPorts,
	}, nil
}

// StopInstance stops and removes a standalone container or Swarm service.
func (s *Service) StopInstance(ctx context.Context, containerID string) error {
	if strings.HasPrefix(containerID, swarmRuntimePrefix) {
		return s.stopSwarmInstance(ctx, containerID)
	}
	timeout := 10 // seconds
	if _, err := s.client.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		s.logger.Warn("ContainerStop returned error; force-removing anyway",
			zap.String("container", containerID), zap.Error(err))
	}
	_, err := s.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil && (cerrdefs.IsConflict(err) || strings.Contains(strings.ToLower(err.Error()), "removal of container") && strings.Contains(strings.ToLower(err.Error()), "already in progress")) {
		return s.waitForContainerRemoval(ctx, containerID, err)
	}
	return err
}

func (s *Service) waitForContainerRemoval(ctx context.Context, containerID string, originalErr error) error {
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := s.client.ContainerInspect(waitCtx, containerID, client.ContainerInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-waitCtx.Done():
			return originalErr
		case <-ticker.C:
		}
	}
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

	authStr := s.registryAuthFor(ctx, image)
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
	// The config.json "auth" field is base64(user:pass) - decode and split.
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
	limit = strings.TrimSpace(strings.ToLower(limit))
	if limit == "" {
		return 0, nil
	}
	if strings.HasSuffix(limit, "m") {
		milliCPU, err := strconv.ParseInt(strings.TrimSuffix(limit, "m"), 10, 64)
		if err != nil || milliCPU <= 0 {
			return 0, fmt.Errorf("must be a positive CPU count or millicpu value")
		}
		return milliCPU * 1_000_000, nil
	}
	cpus, err := strconv.ParseFloat(limit, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a CPU count: %w", err)
	}
	if cpus <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return int64(cpus * 1e9), nil
}

// parseMemoryLimit parses memory limit string to bytes
func parseMemoryLimit(limit string) (int64, error) {
	if limit == "" {
		return 0, nil
	}

	limit = strings.TrimSpace(strings.ToLower(limit))
	units := []struct {
		suffix     string
		multiplier int64
	}{
		{"gib", 1024 * 1024 * 1024},
		{"gb", 1024 * 1024 * 1024},
		{"gi", 1024 * 1024 * 1024},
		{"g", 1024 * 1024 * 1024},
		{"mib", 1024 * 1024},
		{"mb", 1024 * 1024},
		{"mi", 1024 * 1024},
		{"m", 1024 * 1024},
		{"kib", 1024},
		{"kb", 1024},
		{"ki", 1024},
		{"k", 1024},
	}
	multiplier := int64(1)
	valueText := limit
	for _, unit := range units {
		if strings.HasSuffix(limit, unit.suffix) {
			multiplier = unit.multiplier
			valueText = strings.TrimSpace(strings.TrimSuffix(limit, unit.suffix))
			break
		}
	}
	value, err := strconv.ParseInt(valueText, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be an integer byte quantity: %w", err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return value * multiplier, nil
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
