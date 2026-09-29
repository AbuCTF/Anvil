// Command docker-tcp-router exposes raw-TCP Docker challenges on a single host.
//
// Challenge containers stay on an internal-only bridge, so Docker deliberately
// does not activate their HostConfig port bindings. This trusted host-network
// process discovers Anvil's route labels through the Docker socket and forwards
// each configured pool port to the container IP. The bidirectional copy waits
// for both directions and propagates CloseWrite, preserving the half-close
// behavior used by pwn and blockchain clients.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	anvilcontainer "github.com/anvil-lab/anvil/internal/services/container"
	"github.com/moby/moby/client"
)

type routeTable struct {
	docker  *client.Client
	minPort int
	maxPort int

	mu       sync.RWMutex
	backends map[int]string
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseRange(value string) (int, int, error) {
	left, right, ok := strings.Cut(strings.TrimSpace(value), "-")
	if !ok {
		return 0, 0, fmt.Errorf("expected MIN-MAX")
	}
	start, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil {
		return 0, 0, fmt.Errorf("parse minimum: %w", err)
	}
	end, err := strconv.Atoi(strings.TrimSpace(right))
	if err != nil {
		return 0, 0, fmt.Errorf("parse maximum: %w", err)
	}
	if start < 1024 || end > 65535 || end < start {
		return 0, 0, fmt.Errorf("range must be within 1024-65535 and ordered")
	}
	return start, end, nil
}

func parseDuration(key, fallback string) time.Duration {
	value := env(key, fallback)
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		log.Fatalf("%s=%q is not a positive duration", key, value)
	}
	return duration
}

func (r *routeTable) load(ctx context.Context) error {
	result, err := r.docker.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("label", anvilcontainer.TCPRoutesLabel),
	})
	if err != nil {
		return fmt.Errorf("list routed containers: %w", err)
	}

	next := make(map[int]string)
	conflicts := make(map[int]struct{})
	for _, summary := range result.Items {
		if summary.State != "running" || summary.Labels["anvil.instance.id"] == "" {
			continue
		}
		var declared map[string]int
		if err := json.Unmarshal([]byte(summary.Labels[anvilcontainer.TCPRoutesLabel]), &declared); err != nil {
			log.Printf("ignore container %.12s with invalid route label: %v", summary.ID, err)
			continue
		}
		networkName := strings.TrimSpace(summary.Labels[anvilcontainer.TCPNetworkLabel])
		if networkName == "" {
			log.Printf("ignore container %.12s without TCP network label", summary.ID)
			continue
		}
		inspectResult, err := r.docker.ContainerInspect(ctx, summary.ID, client.ContainerInspectOptions{})
		if err != nil {
			log.Printf("inspect container %.12s: %v", summary.ID, err)
			continue
		}
		if inspectResult.Container.NetworkSettings == nil {
			continue
		}
		endpoint, ok := inspectResult.Container.NetworkSettings.Networks[networkName]
		if !ok || !endpoint.IPAddress.IsValid() {
			log.Printf("ignore container %.12s without an address on %s", summary.ID, networkName)
			continue
		}
		for publicRaw, targetPort := range declared {
			publicPort, err := strconv.Atoi(publicRaw)
			if err != nil || publicPort < r.minPort || publicPort > r.maxPort || targetPort < 1 || targetPort > 65535 {
				log.Printf("ignore invalid route %q:%d on container %.12s", publicRaw, targetPort, summary.ID)
				continue
			}
			if _, conflict := conflicts[publicPort]; conflict {
				continue
			}
			if previous, exists := next[publicPort]; exists {
				delete(next, publicPort)
				conflicts[publicPort] = struct{}{}
				log.Printf("disable conflicting route on :%d (%s and container %.12s)", publicPort, previous, summary.ID)
				continue
			}
			next[publicPort] = net.JoinHostPort(endpoint.IPAddress.String(), strconv.Itoa(targetPort))
		}
	}

	r.mu.Lock()
	r.backends = next
	r.mu.Unlock()
	return nil
}

func (r *routeTable) backend(port int) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.backends[port]
}

func (r *routeTable) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.backends)
}

func (r *routeTable) refreshLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.load(ctx); err != nil {
				log.Printf("refresh routes: %v", err)
			}
		}
	}
}

func closeWrite(connection net.Conn) {
	if closer, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func pipe(clientConn, backendConn net.Conn) {
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(backendConn, clientConn)
		closeWrite(backendConn)
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(clientConn, backendConn)
		closeWrite(clientConn)
	}()
	wait.Wait()
	_ = clientConn.Close()
	_ = backendConn.Close()
}

func handleConnection(table *routeTable, clientConn net.Conn, publicPort int, dialTimeout, maxLifetime time.Duration) {
	backendAddress := table.backend(publicPort)
	if backendAddress == "" {
		_ = clientConn.Close()
		return
	}
	backendConn, err := net.DialTimeout("tcp", backendAddress, dialTimeout)
	if err != nil {
		log.Printf("dial backend %s for :%d: %v", backendAddress, publicPort, err)
		_ = clientConn.Close()
		return
	}
	deadline := time.Now().Add(maxLifetime)
	_ = clientConn.SetDeadline(deadline)
	_ = backendConn.SetDeadline(deadline)
	pipe(clientConn, backendConn)
}

func listen(ctx context.Context, table *routeTable, address string, port int, dialTimeout, maxLifetime time.Duration) (net.Listener, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen on %s:%d: %w", address, port, err)
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("accept on :%d: %v", port, err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			go handleConnection(table, connection, port, dialTimeout, maxLifetime)
		}
	}()
	return listener, nil
}

func runHealthcheck() {
	address := env("TCP_ROUTER_HEALTH_ADDR", "127.0.0.1:18083")
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address + "/healthz")
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Fatalf("health returned %s", response.Status)
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-healthcheck" {
		runHealthcheck()
		return
	}

	minPort, maxPort, err := parseRange(env("TCP_ROUTER_PORT_RANGE", "30000-30199"))
	if err != nil {
		log.Fatalf("TCP_ROUTER_PORT_RANGE: %v", err)
	}
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("create Docker client: %v", err)
	}
	defer dockerClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	table := &routeTable{docker: dockerClient, minPort: minPort, maxPort: maxPort, backends: make(map[int]string)}
	if err := table.load(ctx); err != nil {
		log.Fatalf("load initial routes: %v", err)
	}

	listenAddress := env("TCP_ROUTER_LISTEN_ADDRESS", "0.0.0.0")
	dialTimeout := parseDuration("TCP_ROUTER_DIAL_TIMEOUT", "5s")
	maxLifetime := parseDuration("TCP_ROUTER_MAX_LIFETIME", "30m")
	listeners := make([]net.Listener, 0, maxPort-minPort+1)
	for port := minPort; port <= maxPort; port++ {
		listener, err := listen(ctx, table, listenAddress, port, dialTimeout, maxLifetime)
		if err != nil {
			for _, openListener := range listeners {
				_ = openListener.Close()
			}
			log.Fatal(err)
		}
		listeners = append(listeners, listener)
	}

	refreshInterval := parseDuration("TCP_ROUTER_REFRESH", "1s")
	go table.refreshLoop(ctx, refreshInterval)

	healthAddress := env("TCP_ROUTER_HEALTH_ADDR", "127.0.0.1:18083")
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"status":"healthy","routes":%d}`+"\n", table.count())
	})
	healthServer := &http.Server{Addr: healthAddress, Handler: healthMux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health server: %v", err)
			stop()
		}
	}()

	log.Printf("Docker TCP router listening on %s:%d-%d", listenAddress, minPort, maxPort)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = healthServer.Shutdown(shutdownCtx)
}
