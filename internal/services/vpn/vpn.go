package vpn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"go.uber.org/zap"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Service handles VPN configuration and management
type Service struct {
	config config.VPNConfig
	logger *zap.Logger

	// IP allocation
	ipMu      sync.Mutex
	usedIPs   map[string]bool
	ipNetwork *net.IPNet
	nextIP    net.IP
}

// NewService creates a new VPN service
func NewService(cfg config.VPNConfig, logger *zap.Logger) (*Service, error) {
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
		logger:    logger,
		usedIPs:   make(map[string]bool),
		ipNetwork: ipNet,
		nextIP:    startIP,
	}

	return s, nil
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
	// AllowedIPs for client - route challenge network traffic through VPN
	allowedIPs := s.config.AddressRange

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = %s
MTU = %d

[Peer]
PublicKey = %s
AllowedIPs = %s
Endpoint = %s:%d
PersistentKeepalive = 25
`,
		privateKey,
		assignedIP,
		s.config.DNS,
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
	// This would use wgctrl to add the peer dynamically
	// For now, we'll log the operation
	s.logger.Info("Adding VPN peer",
		zap.String("public_key", publicKey[:8]+"..."),
		zap.String("assigned_ip", assignedIP),
	)

	// In a real implementation:
	// 1. Use wgctrl to add the peer
	// 2. Or write to the WireGuard config and reload
	// 3. Or use the wg command-line tool

	return nil
}

// RemovePeer removes a peer from the WireGuard server
func (s *Service) RemovePeer(ctx context.Context, publicKey string) error {
	s.logger.Info("Removing VPN peer",
		zap.String("public_key", publicKey[:8]+"..."),
	)

	// Similar to AddPeer, this would use wgctrl or the wg command
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
