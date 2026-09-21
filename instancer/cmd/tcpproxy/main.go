// Command tcpproxy is a half-close-preserving L4 TCP proxy for raw-TCP challenge
// instances (pwn / web3). Traefik's IngressRouteTCP drops the server's reply the
// moment a client half-closes (shutdown(SHUT_WR)) — the standard pwn/blockchain
// solve pattern — so no framework challenge can deliver its flag through it. This
// proxy instead copies both directions and waits for BOTH to finish before
// closing, propagating half-close (CloseWrite) each way, so `nc host port` + a
// SHUT_WR solve gets its flag back.
//
// Routing: each pool port maps 1:1 to a challenge instance. The mapping lives in
// the port-lock ConfigMaps the instancer controller writes (labeled
// instancer.anvil.dev/port-pool=true, data {port, instance, backend}). The proxy
// keeps a refreshed cache of port->backend and dials the backend ClusterIP
// service directly (kube-proxy L4, which preserves half-close end to end).
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	portPoolLabel     = "instancer.anvil.dev/port-pool"
	portInstanceLabel = "instancer.anvil.dev/instance"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseRange(s string) (int, int) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return 0, 0
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(a))
	end, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || start <= 0 || end < start {
		return 0, 0
	}
	return start, end
}

// router holds the current port->backend map, refreshed from the lock ConfigMaps.
type router struct {
	cs        *kubernetes.Clientset
	namespace string
	mu        sync.RWMutex
	backends  map[int]string
}

func (r *router) load(ctx context.Context) error {
	list, err := r.cs.CoreV1().ConfigMaps(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: portPoolLabel + "=true",
	})
	if err != nil {
		return err
	}
	next := make(map[int]string, len(list.Items))
	for i := range list.Items {
		cm := &list.Items[i]
		port, err := strconv.Atoi(cm.Data["port"])
		if err != nil {
			continue
		}
		if be := cm.Data["backend"]; be != "" {
			next[port] = be
		}
	}
	r.mu.Lock()
	r.backends = next
	r.mu.Unlock()
	return nil
}

func (r *router) backend(port int) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.backends[port]
}

func (r *router) refreshLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.load(ctx); err != nil {
				klog.Warningf("refresh lock cache: %v", err)
			}
		}
	}
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// pipe copies client<->backend, propagating half-close and waiting for BOTH
// directions before closing. This is the fix for the Traefik behavior: a client
// SHUT_WR must not tear down the server->client path before the reply arrives.
func pipe(client, backend net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(backend, client); closeWrite(backend) }()
	go func() { defer wg.Done(); _, _ = io.Copy(client, backend); closeWrite(client) }()
	wg.Wait()
	_ = client.Close()
	_ = backend.Close()
}

func (r *router) handle(client net.Conn, port int, dialTimeout, maxLife time.Duration) {
	defer func() {
		if rec := recover(); rec != nil {
			klog.Errorf("panic handling :%d: %v", port, rec)
		}
	}()
	be := r.backend(port)
	if be == "" {
		// no instance holds this port right now; nothing to serve
		_ = client.Close()
		return
	}
	backend, err := net.DialTimeout("tcp", be, dialTimeout)
	if err != nil {
		klog.Warningf("dial backend %s for :%d: %v", be, port, err)
		_ = client.Close()
		return
	}
	// coarse absolute lifetime guard so a wedged connection cannot leak forever;
	// challenge servers close right after writing the flag, so this never bites
	// a healthy solve.
	deadline := time.Now().Add(maxLife)
	_ = client.SetDeadline(deadline)
	_ = backend.SetDeadline(deadline)
	pipe(client, backend)
}

func listen(ctx context.Context, r *router, port int, dialTimeout, maxLife time.Duration) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", port, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					klog.Warningf("accept :%d: %v", port, err)
					time.Sleep(100 * time.Millisecond)
					continue
				}
			}
			go r.handle(conn, port, dialTimeout, maxLife)
		}
	}()
	return nil
}

func main() {
	ns := env("PROXY_POOL_NAMESPACE", "anvil-instancer")
	start, end := parseRange(env("PROXY_POOL_RANGE", "30000-30063"))
	if start == 0 {
		klog.Fatalf("PROXY_POOL_RANGE invalid or empty")
	}
	refresh, _ := time.ParseDuration(env("PROXY_REFRESH", "3s"))
	dialTimeout, _ := time.ParseDuration(env("PROXY_DIAL_TIMEOUT", "5s"))
	maxLife, _ := time.ParseDuration(env("PROXY_MAX_LIFETIME", "30m"))

	rc, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("in-cluster config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		klog.Fatalf("clientset: %v", err)
	}

	r := &router{cs: cs, namespace: ns, backends: map[int]string{}}
	ctx := context.Background()
	if err := r.load(ctx); err != nil {
		klog.Warningf("initial lock cache load: %v", err)
	}
	go r.refreshLoop(ctx, refresh)

	for port := start; port <= end; port++ {
		if err := listen(ctx, r, port, dialTimeout, maxLife); err != nil {
			klog.Fatalf("%v", err)
		}
	}
	klog.Infof("tcpproxy listening on :%d-%d, backends from ns %q (refresh %s)", start, end, ns, refresh)
	select {}
}
