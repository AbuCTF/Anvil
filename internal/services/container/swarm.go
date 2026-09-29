package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
)

const swarmRuntimePrefix = "swarm:"

var safeSwarmObjectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type swarmRuntimeID struct {
	serviceID   string
	networkName string
}

func encodeSwarmRuntimeID(serviceID, networkName string) string {
	return swarmRuntimePrefix + serviceID + ":" + networkName
}

func parseSwarmRuntimeID(value string) (swarmRuntimeID, error) {
	parts := strings.SplitN(value, ":", 3)
	if len(parts) != 3 || parts[0] != strings.TrimSuffix(swarmRuntimePrefix, ":") {
		return swarmRuntimeID{}, fmt.Errorf("invalid Swarm runtime identifier")
	}
	if !safeSwarmObjectName.MatchString(parts[1]) || !safeSwarmObjectName.MatchString(parts[2]) {
		return swarmRuntimeID{}, fmt.Errorf("invalid Swarm runtime identifier")
	}
	return swarmRuntimeID{serviceID: parts[1], networkName: parts[2]}, nil
}

func normalizeSwarmArchitecture(platform, fallback string) (string, error) {
	value := strings.TrimSpace(platform)
	if parsed := parsePlatform(value); parsed != nil {
		value = parsed.Architecture
	}
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	switch strings.ToLower(value) {
	case "amd64", "x86_64", "x86-64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported Swarm architecture %q", value)
	}
}

func swarmHTTPRouteDocument(routerName, host, scheme string, publishedPort int) ([]byte, error) {
	if !safeSwarmObjectName.MatchString(routerName) {
		return nil, fmt.Errorf("invalid route name %q", routerName)
	}
	if !validHostname(host) {
		return nil, fmt.Errorf("invalid HTTP route hostname %q", host)
	}
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("invalid backend scheme %q", scheme)
	}
	if publishedPort < 1 || publishedPort > 65535 {
		return nil, fmt.Errorf("invalid published HTTP port %d", publishedPort)
	}

	rule := fmt.Sprintf("Host(`%s`)", host)
	return []byte(fmt.Sprintf(
		"http:\n"+
			"  routers:\n"+
			"    %s:\n"+
			"      entryPoints:\n"+
			"        - web\n"+
			"      rule: %q\n"+
			"      service: %s\n"+
			"  services:\n"+
			"    %s:\n"+
			"      loadBalancer:\n"+
			"        servers:\n"+
			"          - url: %q\n",
		routerName,
		rule,
		routerName,
		routerName,
		fmt.Sprintf("%s://127.0.0.1:%d", scheme, publishedPort),
	)), nil
}

func swarmRoutePath(routesPath, networkName string) (string, error) {
	if strings.TrimSpace(routesPath) == "" {
		return "", errors.New("Swarm HTTP routes path is empty")
	}
	if !safeSwarmObjectName.MatchString(networkName) {
		return "", fmt.Errorf("invalid Swarm network name %q", networkName)
	}
	return filepath.Join(routesPath, networkName+".yaml"), nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".anvil-route-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *Service) createSwarmInstance(ctx context.Context, req CreateInstanceRequest, image string) (*CreateInstanceResponse, error) {
	if err := s.ensureSwarmManager(ctx); err != nil {
		return nil, err
	}
	if req.PublicHost != "" && !validHostname(req.PublicHost) {
		return nil, fmt.Errorf("invalid public hostname %q", req.PublicHost)
	}

	cpuLimit, err := parseCPULimit(req.CPULimit)
	if err != nil {
		return nil, fmt.Errorf("invalid CPU limit: %w", err)
	}
	memoryLimit, err := parseMemoryLimit(req.MemoryLimit)
	if err != nil {
		return nil, fmt.Errorf("invalid memory limit: %w", err)
	}
	architecture, err := normalizeSwarmArchitecture(req.Platform, s.config.SwarmDefaultArch)
	if err != nil {
		return nil, err
	}

	shortID := strings.ReplaceAll(req.InstanceID.String(), "-", "")[:12]
	serviceName := fmt.Sprintf("anvil-%s-%s", req.ChallengeSlug, shortID)
	networkName := "anvil-i-" + shortID
	if !safeSwarmObjectName.MatchString(serviceName) {
		return nil, fmt.Errorf("challenge slug produces invalid Swarm service name")
	}

	labels := make(map[string]string, len(s.config.Labels)+4)
	for key, value := range s.config.Labels {
		labels[key] = value
	}
	for key, value := range req.Labels {
		labels[key] = value
	}
	labels["managed-by"] = "anvil"
	labels["anvil.instance.id"] = req.InstanceID.String()
	labels["anvil.challenge.slug"] = req.ChallengeSlug
	labels["anvil.swarm.network"] = networkName

	networkResult, err := s.client.NetworkCreate(ctx, networkName, client.NetworkCreateOptions{
		Driver:     "overlay",
		Scope:      "swarm",
		Internal:   s.config.NetworkInternal,
		Attachable: false,
		Labels: map[string]string{
			"managed-by":           "anvil",
			"anvil.instance.id":    req.InstanceID.String(),
			"anvil.challenge.slug": req.ChallengeSlug,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create isolated Swarm network: %w", err)
	}

	cleanup := func(serviceID string) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if serviceID != "" {
			_, _ = s.client.ServiceRemove(cleanupCtx, serviceID, client.ServiceRemoveOptions{})
		}
		_ = s.removeSwarmNetwork(cleanupCtx, networkName)
	}

	publishedPorts := make(map[string]int)
	var endpointPorts []swarm.PortConfig
	var httpTarget *ExposedPort
	var httpPublishedPort int

	s.portMu.Lock()
	used, err := s.usedSwarmPorts(ctx)
	if err == nil {
		for index := range req.ExposedPorts {
			exposed := req.ExposedPorts[index]
			if exposed.Port < 1 || exposed.Port > 65535 {
				err = fmt.Errorf("invalid exposed port %d", exposed.Port)
				break
			}
			if isHTTPPort(exposed) {
				if httpTarget != nil {
					continue
				}
				if !s.config.HTTPRouting || req.PublicHost == "" {
					continue
				}
				httpTarget = &req.ExposedPorts[index]
				httpPublishedPort, err = nextAvailableTCPPort(
					s.config.SwarmHTTPPortMin,
					s.config.SwarmHTTPPortMax,
					used,
					nil,
				)
				if err != nil {
					break
				}
				endpointPorts = append(endpointPorts, swarm.PortConfig{
					Name:          fmt.Sprintf("http-%d", exposed.Port),
					Protocol:      network.TCP,
					TargetPort:    uint32(exposed.Port),
					PublishedPort: uint32(httpPublishedPort),
					PublishMode:   swarm.PortConfigPublishModeIngress,
				})
				continue
			}
			if !isRawTCPPort(exposed) || !s.config.TCPRouting {
				continue
			}
			var publicPort int
			publicPort, err = nextAvailableTCPPort(
				s.config.TCPPortMin,
				s.config.TCPPortMax,
				used,
				nil,
			)
			if err != nil {
				break
			}
			publishedPorts[fmt.Sprintf("%d/tcp", exposed.Port)] = publicPort
			endpointPorts = append(endpointPorts, swarm.PortConfig{
				Name:          fmt.Sprintf("tcp-%d", exposed.Port),
				Protocol:      network.TCP,
				TargetPort:    uint32(exposed.Port),
				PublishedPort: uint32(publicPort),
				PublishMode:   swarm.PortConfigPublishModeIngress,
			})
		}
	}
	if err != nil {
		s.portMu.Unlock()
		cleanup("")
		return nil, fmt.Errorf("allocate Swarm port: %w", err)
	}

	replicas := uint64(1)
	maxAttempts := uint64(3)
	delay := time.Second
	resources := &swarm.ResourceRequirements{
		Limits: &swarm.Limit{
			NanoCPUs:    cpuLimit,
			MemoryBytes: memoryLimit,
		},
		Reservations: &swarm.Resources{
			NanoCPUs:    cpuLimit,
			MemoryBytes: memoryLimit,
		},
	}
	spec := swarm.ServiceSpec{
		Annotations: swarm.Annotations{Name: serviceName, Labels: labels},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image:  image,
				Labels: labels,
				Env:    req.EnvironmentVars,
			},
			Resources: resources,
			RestartPolicy: &swarm.RestartPolicy{
				Condition:   swarm.RestartPolicyConditionOnFailure,
				Delay:       &delay,
				MaxAttempts: &maxAttempts,
			},
			Placement: &swarm.Placement{
				Constraints: []string{
					"node.labels.anvil.runtime==docker",
					"node.labels.anvil.arch==" + architecture,
				},
				Platforms: []swarm.Platform{{OS: "linux", Architecture: architecture}},
			},
			Networks: []swarm.NetworkAttachmentConfig{{
				Target:  networkResult.ID,
				Aliases: []string{"challenge"},
			}},
			LogDriver: &swarm.Driver{
				Name: "json-file",
				Options: map[string]string{
					"max-size": "10m",
					"max-file": "3",
				},
			},
		},
		Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		EndpointSpec: &swarm.EndpointSpec{
			Mode:  swarm.ResolutionModeVIP,
			Ports: endpointPorts,
		},
	}
	createResult, createErr := s.client.ServiceCreate(ctx, client.ServiceCreateOptions{
		Spec:                spec,
		EncodedRegistryAuth: getRegistryAuth(image),
		QueryRegistry:       true,
	})
	s.portMu.Unlock()
	if createErr != nil {
		cleanup("")
		return nil, fmt.Errorf("create Swarm service: %w", createErr)
	}

	task, err := s.waitForSwarmTask(ctx, createResult.ID, 3*time.Minute)
	if err != nil {
		cleanup(createResult.ID)
		return nil, err
	}

	if httpTarget != nil {
		backendScheme := "http"
		if strings.EqualFold(httpTarget.Service, "https") || strings.EqualFold(httpTarget.Protocol, "https") {
			backendScheme = "https"
		}
		routeData, routeErr := swarmHTTPRouteDocument(networkName, req.PublicHost, backendScheme, httpPublishedPort)
		if routeErr != nil {
			cleanup(createResult.ID)
			return nil, routeErr
		}
		routePath, routeErr := swarmRoutePath(s.config.HTTPRoutesPath, networkName)
		if routeErr == nil {
			routeErr = writeFileAtomically(routePath, routeData, 0o640)
		}
		if routeErr != nil {
			cleanup(createResult.ID)
			return nil, fmt.Errorf("publish Swarm HTTP route: %w", routeErr)
		}
	}

	ipAddress := ""
	for _, attachment := range task.NetworksAttachments {
		if attachment.Network.ID != networkResult.ID || len(attachment.Addresses) == 0 {
			continue
		}
		ipAddress = attachment.Addresses[0].Addr().String()
		break
	}
	runtimeID := encodeSwarmRuntimeID(createResult.ID, networkName)
	s.logger.Info("Created Swarm challenge service",
		zap.String("service_id", createResult.ID),
		zap.String("name", serviceName),
		zap.String("network", networkName),
		zap.String("node_id", task.NodeID),
		zap.String("architecture", architecture),
	)
	return &CreateInstanceResponse{
		ContainerID:    runtimeID,
		ContainerName:  serviceName,
		IPAddress:      ipAddress,
		PublicHost:     req.PublicHost,
		PublishedPorts: publishedPorts,
	}, nil
}

func (s *Service) usedSwarmPorts(ctx context.Context) (map[int]struct{}, error) {
	used, err := s.usedDockerTCPPorts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local Docker ports: %w", err)
	}
	services, err := s.client.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Swarm services: %w", err)
	}
	for _, service := range services.Items {
		for _, port := range service.Endpoint.Ports {
			if port.PublishedPort != 0 {
				used[int(port.PublishedPort)] = struct{}{}
			}
		}
		if service.Spec.EndpointSpec == nil {
			continue
		}
		for _, port := range service.Spec.EndpointSpec.Ports {
			if port.PublishedPort != 0 {
				used[int(port.PublishedPort)] = struct{}{}
			}
		}
	}
	return used, nil
}

func (s *Service) waitForSwarmTask(ctx context.Context, serviceID string, timeout time.Duration) (swarm.Task, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	lastState := swarm.TaskState("")
	lastMessage := ""
	for {
		result, err := s.client.TaskList(waitCtx, client.TaskListOptions{
			Filters: make(client.Filters).Add("service", serviceID),
		})
		if err != nil {
			return swarm.Task{}, fmt.Errorf("list Swarm service tasks: %w", err)
		}
		terminalTasks := 0
		for _, task := range result.Items {
			lastState = task.Status.State
			lastMessage = task.Status.Err
			if lastMessage == "" {
				lastMessage = task.Status.Message
			}
			switch task.Status.State {
			case swarm.TaskStateRunning:
				return task, nil
			case swarm.TaskStateRejected, swarm.TaskStateFailed, swarm.TaskStateOrphaned:
				terminalTasks++
			}
		}
		if terminalTasks >= 3 {
			return swarm.Task{}, fmt.Errorf("Swarm task failed after %d attempts (last state %s: %s)", terminalTasks, lastState, lastMessage)
		}
		select {
		case <-waitCtx.Done():
			return swarm.Task{}, fmt.Errorf("Swarm task did not start before timeout (last state %s: %s): %w", lastState, lastMessage, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) stopSwarmInstance(ctx context.Context, runtimeValue string) error {
	runtimeID, err := parseSwarmRuntimeID(runtimeValue)
	if err != nil {
		return err
	}
	if s.config.HTTPRoutesPath != "" {
		path, pathErr := swarmRoutePath(s.config.HTTPRoutesPath, runtimeID.networkName)
		if pathErr != nil {
			return pathErr
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove Swarm HTTP route: %w", removeErr)
		}
	}
	if _, err := s.client.ServiceRemove(ctx, runtimeID.serviceID, client.ServiceRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("remove Swarm service: %w", err)
	}
	if err := s.removeSwarmNetwork(ctx, runtimeID.networkName); err != nil {
		return fmt.Errorf("remove Swarm network: %w", err)
	}
	return nil
}

func (s *Service) removeSwarmNetwork(ctx context.Context, networkName string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	var lastErr error
	for {
		_, err := s.client.NetworkRemove(ctx, networkName, client.NetworkRemoveOptions{})
		if err == nil || errdefs.IsNotFound(err) {
			return nil
		}
		lastErr = err
		if !errdefs.IsConflict(err) && !strings.Contains(strings.ToLower(err.Error()), "active endpoint") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return lastErr
		case <-ticker.C:
		}
	}
}
