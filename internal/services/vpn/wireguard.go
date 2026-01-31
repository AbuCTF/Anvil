package vpn

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/crypto/curve25519"
)

// WireGuardManager handles WireGuard VPN configuration and peer management
type WireGuardManager struct {
	db             *sql.DB
	configDir      string
	interfaceName  string
	serverEndpoint string
	serverPort     int
	serverPrivKey  string
	serverPubKey   string
	networkCIDR    string // e.g., "10.100.0.0/16"
	mu             sync.RWMutex
	peers          map[string]*Peer
}

// Peer represents a WireGuard peer (user)
type Peer struct {
	UserID        string    `json:"user_id"`
	PublicKey     string    `json:"public_key"`
	PrivateKey    string    `json:"-"`           // Never expose in API
	AllowedIPs    string    `json:"allowed_ips"` // User's /24 subnet
	AssignedIP    string    `json:"assigned_ip"` // User's VPN IP
	LastHandshake time.Time `json:"last_handshake"`
	CreatedAt     time.Time `json:"created_at"`
}

// VPNConfig represents the WireGuard config file for a user
type VPNConfig struct {
	Interface InterfaceConfig
	Peer      PeerConfig
}

// InterfaceConfig is the [Interface] section
type InterfaceConfig struct {
	PrivateKey string
	Address    string
	DNS        string
}

// PeerConfig is the [Peer] section for the server
type PeerConfig struct {
	PublicKey           string
	Endpoint            string
	AllowedIPs          string
	PersistentKeepalive int
}

// Config holds WireGuard manager configuration
type Config struct {
	ConfigDir      string
	InterfaceName  string
	ServerEndpoint string
	ServerPort     int
	NetworkCIDR    string
}

// NewWireGuardManager creates a new WireGuard manager
func NewWireGuardManager(db *sql.DB, config *Config) (*WireGuardManager, error) {
	if config.ConfigDir == "" {
		config.ConfigDir = "/etc/wireguard"
	}
	if config.InterfaceName == "" {
		config.InterfaceName = "wg-anvil"
	}
	if config.ServerPort == 0 {
		config.ServerPort = 51820
	}
	if config.NetworkCIDR == "" {
		config.NetworkCIDR = "10.100.0.0/16"
	}

	mgr := &WireGuardManager{
		db:             db,
		configDir:      config.ConfigDir,
		interfaceName:  config.InterfaceName,
		serverEndpoint: config.ServerEndpoint,
		serverPort:     config.ServerPort,
		networkCIDR:    config.NetworkCIDR,
		peers:          make(map[string]*Peer),
	}

	// Initialize or load server keys
	if err := mgr.initServerKeys(); err != nil {
		return nil, fmt.Errorf("failed to initialize server keys: %w", err)
	}

	// Load existing peers
	if err := mgr.loadPeers(); err != nil {
		return nil, fmt.Errorf("failed to load peers: %w", err)
	}

	return mgr, nil
}

// initServerKeys generates or loads server keys
func (m *WireGuardManager) initServerKeys() error {
	keyPath := filepath.Join(m.configDir, m.interfaceName+".key")
	pubPath := filepath.Join(m.configDir, m.interfaceName+".pub")

	// Check if keys exist
	if _, err := os.Stat(keyPath); err == nil {
		// Load existing keys
		privKey, err := os.ReadFile(keyPath)
		if err != nil {
			return err
		}
		pubKey, err := os.ReadFile(pubPath)
		if err != nil {
			return err
		}
		m.serverPrivKey = strings.TrimSpace(string(privKey))
		m.serverPubKey = strings.TrimSpace(string(pubKey))
		return nil
	}

	// Generate new keys
	privKey, pubKey, err := generateKeyPair()
	if err != nil {
		return err
	}

	m.serverPrivKey = privKey
	m.serverPubKey = pubKey

	// Save keys
	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(privKey), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, []byte(pubKey), 0644); err != nil {
		return err
	}

	// Generate server config
	return m.generateServerConfig()
}

// generateServerConfig creates the WireGuard server configuration
func (m *WireGuardManager) generateServerConfig() error {
	configPath := filepath.Join(m.configDir, m.interfaceName+".conf")

	config := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = 10.100.0.1/16
ListenPort = %d
PostUp = iptables -A FORWARD -i %s -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i %s -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE

`, m.serverPrivKey, m.serverPort, m.interfaceName, m.interfaceName)

	return os.WriteFile(configPath, []byte(config), 0600)
}

// loadPeers loads existing peers from database
func (m *WireGuardManager) loadPeers() error {
	rows, err := m.db.Query(`
		SELECT user_id, public_key, allowed_ips, assigned_ip, created_at
		FROM vpn_peers
	`)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			// Table doesn't exist yet, that's fine
			return nil
		}
		return err
	}
	defer rows.Close()

	m.mu.Lock()
	defer m.mu.Unlock()

	for rows.Next() {
		peer := &Peer{}
		err := rows.Scan(&peer.UserID, &peer.PublicKey, &peer.AllowedIPs, &peer.AssignedIP, &peer.CreatedAt)
		if err != nil {
			return err
		}
		m.peers[peer.UserID] = peer
	}

	return nil
}

// GenerateUserConfig generates a WireGuard config for a user
func (m *WireGuardManager) GenerateUserConfig(userID string, subnetCIDR string, userIP string) (*VPNConfig, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if user already has a config
	if existing, ok := m.peers[userID]; ok {
		// Return existing config (user needs to re-download if they lost it)
		return nil, "", fmt.Errorf("config already exists, assigned IP: %s", existing.AssignedIP)
	}

	// Generate user keys
	privKey, pubKey, err := generateKeyPair()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate keys: %w", err)
	}

	peer := &Peer{
		UserID:     userID,
		PublicKey:  pubKey,
		PrivateKey: privKey,
		AllowedIPs: subnetCIDR,
		AssignedIP: userIP,
		CreatedAt:  time.Now(),
	}

	// Save to database
	_, err = m.db.Exec(`
		INSERT INTO vpn_peers (user_id, public_key, private_key_encrypted, allowed_ips, assigned_ip, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			public_key = EXCLUDED.public_key,
			private_key_encrypted = EXCLUDED.private_key_encrypted,
			allowed_ips = EXCLUDED.allowed_ips,
			assigned_ip = EXCLUDED.assigned_ip
	`, userID, pubKey, privKey, subnetCIDR, userIP, peer.CreatedAt) // Note: In production, encrypt privKey!

	if err != nil {
		return nil, "", fmt.Errorf("failed to save peer: %w", err)
	}

	m.peers[userID] = peer

	// Add peer to WireGuard interface
	if err := m.addPeerToInterface(peer); err != nil {
		// Non-fatal - can be synced later
		fmt.Printf("Warning: failed to add peer to interface: %v\n", err)
	}

	// Generate user config
	config := &VPNConfig{
		Interface: InterfaceConfig{
			PrivateKey: privKey,
			Address:    userIP + "/24",
			DNS:        "8.8.8.8",
		},
		Peer: PeerConfig{
			PublicKey:           m.serverPubKey,
			Endpoint:            fmt.Sprintf("%s:%d", m.serverEndpoint, m.serverPort),
			AllowedIPs:          subnetCIDR, // User can only access their own subnet
			PersistentKeepalive: 25,
		},
	}

	// Generate config file content
	configContent := m.renderConfig(config)

	return config, configContent, nil
}

// GetUserConfig retrieves existing VPN config for a user
func (m *WireGuardManager) GetUserConfig(userID string) (string, string, error) {
	var privKey, allowedIPs, assignedIP string
	err := m.db.QueryRow(`
		SELECT private_key_encrypted, allowed_ips, assigned_ip
		FROM vpn_peers WHERE user_id = $1
	`, userID).Scan(&privKey, &allowedIPs, &assignedIP)

	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("no VPN config found for user")
	}
	if err != nil {
		return "", "", err
	}

	config := &VPNConfig{
		Interface: InterfaceConfig{
			PrivateKey: privKey,
			Address:    assignedIP + "/24",
			DNS:        "8.8.8.8",
		},
		Peer: PeerConfig{
			PublicKey:           m.serverPubKey,
			Endpoint:            fmt.Sprintf("%s:%d", m.serverEndpoint, m.serverPort),
			AllowedIPs:          allowedIPs,
			PersistentKeepalive: 25,
		},
	}

	return m.renderConfig(config), assignedIP, nil
}

// renderConfig renders a VPN config to string
func (m *WireGuardManager) renderConfig(config *VPNConfig) string {
	const configTemplate = `[Interface]
PrivateKey = {{.Interface.PrivateKey}}
Address = {{.Interface.Address}}
DNS = {{.Interface.DNS}}

[Peer]
PublicKey = {{.Peer.PublicKey}}
Endpoint = {{.Peer.Endpoint}}
AllowedIPs = {{.Peer.AllowedIPs}}
PersistentKeepalive = {{.Peer.PersistentKeepalive}}
`

	tmpl, _ := template.New("config").Parse(configTemplate)
	var buf strings.Builder
	tmpl.Execute(&buf, config)
	return buf.String()
}

// addPeerToInterface adds a peer to the running WireGuard interface
func (m *WireGuardManager) addPeerToInterface(peer *Peer) error {
	cmd := exec.Command("wg", "set", m.interfaceName,
		"peer", peer.PublicKey,
		"allowed-ips", peer.AllowedIPs,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set failed: %w - output: %s", err, string(output))
	}
	return nil
}

// RemovePeer removes a peer from WireGuard
func (m *WireGuardManager) RemovePeer(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	peer, ok := m.peers[userID]
	if !ok {
		return nil
	}

	// Remove from WireGuard interface
	cmd := exec.Command("wg", "set", m.interfaceName, "peer", peer.PublicKey, "remove")
	cmd.Run() // Ignore error

	// Remove from database
	m.db.Exec("DELETE FROM vpn_peers WHERE user_id = $1", userID)

	delete(m.peers, userID)
	return nil
}

// SyncPeers syncs all peers to the WireGuard interface
func (m *WireGuardManager) SyncPeers() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, peer := range m.peers {
		if err := m.addPeerToInterface(peer); err != nil {
			fmt.Printf("Warning: failed to sync peer %s: %v\n", peer.UserID, err)
		}
	}

	return nil
}

// GetStatus returns WireGuard interface status
func (m *WireGuardManager) GetStatus() (map[string]interface{}, error) {
	cmd := exec.Command("wg", "show", m.interfaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wg show failed: %w", err)
	}

	return map[string]interface{}{
		"interface": m.interfaceName,
		"port":      m.serverPort,
		"status":    "active",
		"output":    string(output),
	}, nil
}

// StartInterface starts the WireGuard interface
func (m *WireGuardManager) StartInterface() error {
	cmd := exec.Command("wg-quick", "up", m.interfaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if already up
		if strings.Contains(string(output), "already exists") {
			return nil
		}
		return fmt.Errorf("wg-quick up failed: %w - output: %s", err, string(output))
	}
	return nil
}

// StopInterface stops the WireGuard interface
func (m *WireGuardManager) StopInterface() error {
	cmd := exec.Command("wg-quick", "down", m.interfaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "is not a WireGuard interface") {
			return nil
		}
		return fmt.Errorf("wg-quick down failed: %w - output: %s", err, string(output))
	}
	return nil
}

// generateKeyPair generates a WireGuard key pair
func generateKeyPair() (privateKey, publicKey string, err error) {
	var privKey [32]byte
	if _, err := rand.Read(privKey[:]); err != nil {
		return "", "", err
	}

	// Clamp private key for Curve25519
	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	var pubKey [32]byte
	curve25519.ScalarBaseMult(&pubKey, &privKey)

	return base64.StdEncoding.EncodeToString(privKey[:]),
		base64.StdEncoding.EncodeToString(pubKey[:]),
		nil
}
