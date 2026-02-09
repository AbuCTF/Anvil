package vpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"go.uber.org/zap"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Service handles VPN configuration and management
type Service struct {
	config config.VPNConfig
	db     *database.DB
	logger *zap.Logger

	// IP allocation
	ipMu      sync.Mutex
	usedIPs   map[string]bool
	ipNetwork *net.IPNet
	nextIP    net.IP
}

// NewService creates a new VPN service
func NewService(cfg config.VPNConfig, db *database.DB, logger *zap.Logger) (*Service, error) {
	// Parse the address range
	_, ipNet, err := net.ParseCIDR(cfg.AddressRange)
	if err != nil {
		return nil, fmt.Errorf("invalid address range: %w", err)
	}

	// Calculate first usable IP (skip network address and server IP)
	startIP := make(net.IP, len(ipNet.IP))
	copy(startIP, ipNet.IP)
	incrementIP(startIP) // Skip network address
	incrementIP(startIP) // Skip server address (10.10.0.1)

	s := &Service{
		config:    cfg,
		db:        db,
		logger:    logger,
		usedIPs:   make(map[string]bool),
		ipNetwork: ipNet,
		nextIP:    startIP,
	}

	// Load existing allocated IPs from database
	if err := s.loadAllocatedIPs(); err != nil {
		logger.Warn("failed to load existing VPN IPs", zap.Error(err))
	}

	return s, nil
}

// loadAllocatedIPs loads all allocated IPs from database to prevent conflicts
func (s *Service) loadAllocatedIPs() error {
	rows, err := s.db.Pool.Query(context.Background(), `SELECT assigned_ip FROM vpn_configs`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var highestIP net.IP
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		s.usedIPs[ip] = true

		// Track highest allocated IP to continue from there
		currentIP := net.ParseIP(ip)
		if currentIP != nil {
			if highestIP == nil || bytes.Compare(currentIP, highestIP) > 0 {
				highestIP = currentIP
			}
		}
	}

	// Set nextIP to highest + 1
	if highestIP != nil {
		s.nextIP = make(net.IP, len(highestIP))
		copy(s.nextIP, highestIP)
		incrementIP(s.nextIP)
	}

	s.logger.Info("loaded allocated VPN IPs",
		zap.Int("used_count", len(s.usedIPs)),
		zap.String("next_ip", s.nextIP.String()))

	return rows.Err()
}

// Status returns the VPN service status
func (s *Service) Status() string {
	if !s.config.Enabled {
		return "disabled"
	}
	return "enabled"
}

// GenerateKeyPair generates a new WireGuard key pair
func (s *Service) GenerateKeyPair() (privateKey, publicKey string, err error) {
	// Generate private key
	privKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		// Fallback to manual generation if wgctrl fails
		return s.generateKeyPairManual()
	}

	return privKey.String(), privKey.PublicKey().String(), nil
}

// generateKeyPairManual generates keys without wgctrl (for systems without WireGuard kernel module)
func (s *Service) generateKeyPairManual() (string, string, error) {
	// Generate 32 random bytes for private key
	privKeyBytes := make([]byte, 32)
	if _, err := rand.Read(privKeyBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Clamp the private key (WireGuard requirement)
	privKeyBytes[0] &= 248
	privKeyBytes[31] &= 127
	privKeyBytes[31] |= 64

	privateKeyStr := base64.StdEncoding.EncodeToString(privKeyBytes)

	// Generate public key using curve25519
	// For a proper implementation, we'd use golang.org/x/crypto/curve25519
	// For now, we'll use wgtypes if available
	privKey, err := wgtypes.ParseKey(privateKeyStr)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse generated key: %w", err)
	}

	return privateKeyStr, privKey.PublicKey().String(), nil
}

// AllocateIP allocates a unique IP address for a VPN client
func (s *Service) AllocateIP() (string, error) {
	s.ipMu.Lock()
	defer s.ipMu.Unlock()

	// Find next available IP
	for {
		ipStr := s.nextIP.String()

		// Check if IP is within network range
		if !s.ipNetwork.Contains(s.nextIP) {
			return "", fmt.Errorf("IP address pool exhausted")
		}

		// Check if IP is already used
		if !s.usedIPs[ipStr] {
			s.usedIPs[ipStr] = true

			// Prepare next IP
			incrementIP(s.nextIP)

			return ipStr, nil
		}

		incrementIP(s.nextIP)
	}
}

// ReleaseIP releases an IP address back to the pool
func (s *Service) ReleaseIP(ip string) {
	s.ipMu.Lock()
	defer s.ipMu.Unlock()
	delete(s.usedIPs, ip)
}

// GenerateClientConfig generates a WireGuard client configuration
func (s *Service) GenerateClientConfig(privateKey, assignedIP string) string {
	// AllowedIPs - route challenge networks through VPN
	// 10.100.0.0/16 = VM network (libvirt)
	// 172.20.0.0/16 = Container network (docker)
	allowedIPs := "10.100.0.0/16, 172.20.0.0/16"

	// DNS is optional
	dnsLine := ""
	if s.config.DNS != "" {
		dnsLine = fmt.Sprintf("DNS = %s\n", s.config.DNS)
	}

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
%sMTU = %d

[Peer]
PublicKey = %s
AllowedIPs = %s
Endpoint = %s:%d
PersistentKeepalive = 25
`,
		privateKey,
		assignedIP,
		dnsLine,
		s.config.MTU,
		s.config.PublicKey,
		allowedIPs,
		s.config.PublicEndpoint,
		s.config.ListenPort,
	)
}

// GenerateServerPeerConfig generates the server-side peer configuration for a client
func (s *Service) GenerateServerPeerConfig(publicKey, assignedIP string) string {
	return fmt.Sprintf(`[Peer]
PublicKey = %s
AllowedIPs = %s/32
`,
		publicKey,
		assignedIP,
	)
}

// AddPeer adds a peer to the WireGuard server
func (s *Service) AddPeer(ctx context.Context, publicKey, assignedIP string) error {
	s.logger.Info("Adding VPN peer",
		zap.String("public_key", publicKey[:8]+"..."),
		zap.String("assigned_ip", assignedIP),
	)

	// Execute wg command on host via docker exec to host
	// The container needs to run wg on the host, not inside the container
	// We use SSH to localhost (the host) to execute the command
	cmd := exec.CommandContext(ctx, "ssh", "-o", "StrictHostKeyChecking=no", 
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"root@172.17.0.1",
		fmt.Sprintf("wg set %s peer %s allowed-ips %s/32", s.config.Interface, publicKey, assignedIP))
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.Error("failed to add peer",
			zap.Error(err),
			zap.String("output", string(output)),
			zap.String("public_key", publicKey[:8]+"..."))
		return fmt.Errorf("failed to add peer: %w, output: %s", err, string(output))
	}

	s.logger.Info("Successfully added VPN peer", zap.String("public_key", publicKey[:8]+"..."))
	return nil
}

// RemovePeer removes a peer from the WireGuard server
func (s *Service) RemovePeer(ctx context.Context, publicKey string) error {
	s.logger.Info("Removing VPN peer",
		zap.String("public_key", publicKey[:8]+"..."),
	)

	// Execute wg command on host via SSH
	cmd := exec.CommandContext(ctx, "ssh", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"root@172.17.0.1",
		fmt.Sprintf("wg set %s peer %s remove", s.config.Interface, publicKey))
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.Error("failed to remove peer",
			zap.Error(err),
			zap.String("output", string(output)),
			zap.String("public_key", publicKey[:8]+"..."))
		return fmt.Errorf("failed to remove peer: %w, output: %s", err, string(output))
	}

	s.logger.Info("Successfully removed VPN peer", zap.String("public_key", publicKey[:8]+"..."))
	return nil
}

// GetPeerStatus gets the status of a VPN peer
type PeerStatus struct {
	Connected     bool
	LastHandshake int64 // Unix timestamp
	TransferRx    int64 // Bytes received
	TransferTx    int64 // Bytes transmitted
	Endpoint      string
}

func (s *Service) GetPeerStatus(publicKey string) (*PeerStatus, error) {
	// Parse the public key
	key, err := wgtypes.ParseKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	// Connect to WireGuard via wgctrl
	client, err := wgctrl.New()
	if err != nil {
		s.logger.Warn("failed to connect to wgctrl, returning disconnected", zap.Error(err))
		return &PeerStatus{Connected: false}, nil
	}
	defer client.Close()

	// Get device info
	device, err := client.Device(s.config.Interface)
	if err != nil {
		s.logger.Warn("failed to get WireGuard device", zap.String("interface", s.config.Interface), zap.Error(err))
		return &PeerStatus{Connected: false}, nil
	}

	// Find the peer
	for _, peer := range device.Peers {
		if peer.PublicKey == key {
			var endpoint string
			if peer.Endpoint != nil {
				endpoint = peer.Endpoint.String()
			}

			lastHandshake := peer.LastHandshakeTime.Unix()
			// Consider connected if handshake was within last 3 minutes
			connected := !peer.LastHandshakeTime.IsZero() && time.Since(peer.LastHandshakeTime) < 3*time.Minute

			return &PeerStatus{
				Connected:     connected,
				LastHandshake: lastHandshake,
				TransferRx:    peer.ReceiveBytes,
				TransferTx:    peer.TransmitBytes,
				Endpoint:      endpoint,
			}, nil
		}
	}

	return &PeerStatus{Connected: false}, nil
}

// GetServerPublicKey returns the server's public key
func (s *Service) GetServerPublicKey() string {
	return s.config.PublicKey
}

// GetEndpoint returns the server endpoint
func (s *Service) GetEndpoint() string {
	return fmt.Sprintf("%s:%d", s.config.PublicEndpoint, s.config.ListenPort)
}

// incrementIP increments an IP address by 1
func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// VPNStats returns VPN statistics
type VPNStats struct {
	Enabled        bool
	TotalPeers     int
	ConnectedPeers int
	AllocatedIPs   int
	AvailableIPs   int
}

func (s *Service) Stats() *VPNStats {
	s.ipMu.Lock()
	allocatedIPs := len(s.usedIPs)
	s.ipMu.Unlock()

	// Calculate total available IPs (rough estimate)
	ones, bits := s.ipNetwork.Mask.Size()
	totalIPs := 1<<(bits-ones) - 2 // Subtract network and broadcast

	return &VPNStats{
		Enabled:      s.config.Enabled,
		AllocatedIPs: allocatedIPs,
		AvailableIPs: totalIPs - allocatedIPs,
	}
}
