// Package vm provides virtual machine management using libvirt/QEMU
// Supports OVA, VMDK, QCOW2, and VDI image formats for B2R challenges
// requiring real kernel exploits, systemd, and full network stacks.
package vm

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// VMState represents the current state of a VM
type VMState string

const (
	VMStateNoState      VMState = "nostate"
	VMStateRunning      VMState = "running"
	VMStateBlocked      VMState = "blocked"
	VMStatePaused       VMState = "paused"
	VMStateShutdown     VMState = "shutdown"
	VMStateShutoff      VMState = "shutoff"
	VMStateCrashed      VMState = "crashed"
	VMStatePMSuspended  VMState = "pmsuspended"
	VMStateProvisioning VMState = "provisioning"
	VMStateError        VMState = "error"
)

// ImageFormat represents supported disk image formats
type ImageFormat string

const (
	ImageFormatOVA   ImageFormat = "ova"
	ImageFormatVMDK  ImageFormat = "vmdk"
	ImageFormatQCOW2 ImageFormat = "qcow2"
	ImageFormatVDI   ImageFormat = "vdi"
	ImageFormatRAW   ImageFormat = "raw"
)

// NodeInfo contains connection details for a VM node
type NodeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Hostname    string `json:"hostname"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHKeyPath  string `json:"ssh_key_path"`
	LibvirtURI  string `json:"libvirt_uri"`
	NetworkName string `json:"network_name"`
}

// VMTemplate represents a VM template from an uploaded image
type VMTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	ImagePath   string            `json:"image_path"`
	ImageFormat ImageFormat       `json:"image_format"`
	ImageSize   int64             `json:"image_size"`
	VCPU        int               `json:"vcpu"`
	MemoryMB    int               `json:"memory_mb"`
	DiskGB      int               `json:"disk_gb"`
	OS          string            `json:"os"` // e.g., "ubuntu20.04", "windows10"
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// VMInstance represents a running VM instance
type VMInstance struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	TemplateID   string            `json:"template_id"`
	ChallengeID  string            `json:"challenge_id"`
	UserID       string            `json:"user_id"`
	State        VMState           `json:"state"`
	VCPU         int               `json:"vcpu"`
	MemoryMB     int               `json:"memory_mb"`
	DiskPath     string            `json:"disk_path"` // Path to CoW overlay
	NetworkID    string            `json:"network_id"`
	IPAddress    string            `json:"ip_address"`
	MACAddress   string            `json:"mac_address"`
	VNCPort      int               `json:"vnc_port,omitempty"`
	SSHPort      int               `json:"ssh_port,omitempty"`
	ExposedPorts map[int]int       `json:"exposed_ports"` // guest:host mapping
	Metadata     map[string]string `json:"metadata"`
	Error        string            `json:"error,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at"`
}

// CreateVMRequest contains parameters for creating a new VM
type CreateVMRequest struct {
	Name        string            `json:"name"`
	TemplateID  string            `json:"template_id"`
	ChallengeID string            `json:"challenge_id"`
	UserID      string            `json:"user_id"`
	VCPU        int               `json:"vcpu,omitempty"`      // Override template
	MemoryMB    int               `json:"memory_mb,omitempty"` // Override template
	NetworkID   string            `json:"network_id,omitempty"`
	Duration    time.Duration     `json:"duration"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Service manages virtual machines using libvirt
type Service struct {
	logger       *zap.Logger
	config       Config
	mu           sync.RWMutex
	instances    map[string]*VMInstance
	templates    map[string]*VMTemplate
	usedVNCPorts map[int]bool
	usedIPs      map[string]bool
}

// Config contains VM service configuration
type Config struct {
	ImageStorePath      string // Where VM images are stored
	InstanceStorePath   string // Where instance overlays are stored
	LibvirtURI          string // libvirt connection URI (qemu:///system)
	NetworkName         string // libvirt network name
	NetworkSubnet       string // e.g., "10.100.0.0/16"
	VNCPortStart        int    // Starting port for VNC
	VNCPortEnd          int    // Ending port for VNC
	MaxInstancesPerUser int
	DefaultVCPU         int
	DefaultMemoryMB     int
	DefaultDuration     time.Duration
	MaxDuration         time.Duration
}

// DefaultConfig returns sensible default configuration
func DefaultConfig() Config {
	return Config{
		ImageStorePath:      "/var/lib/anvil/images",
		InstanceStorePath:   "/var/lib/anvil/instances",
		LibvirtURI:          "qemu:///system",
		NetworkName:         "anvil-lab",
		NetworkSubnet:       "10.100.0.0/16",
		VNCPortStart:        5900,
		VNCPortEnd:          6100,
		MaxInstancesPerUser: 2,
		DefaultVCPU:         2,
		DefaultMemoryMB:     2048,
		DefaultDuration:     2 * time.Hour,
		MaxDuration:         8 * time.Hour,
	}
}

// NewService creates a new VM management service
func NewService(logger *zap.Logger, config Config) (*Service, error) {
	// Ensure storage directories exist
	dirs := []string{
		config.ImageStorePath,
		config.InstanceStorePath,
		filepath.Join(config.InstanceStorePath, "overlays"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Verify libvirt/QEMU is available
	if err := verifyLibvirtAvailable(); err != nil {
		logger.Warn("libvirt not available, VM features will be limited", zap.Error(err))
	}

	return &Service{
		logger:       logger,
		config:       config,
		instances:    make(map[string]*VMInstance),
		templates:    make(map[string]*VMTemplate),
		usedVNCPorts: make(map[int]bool),
		usedIPs:      make(map[string]bool),
	}, nil
}

// IsAvailable checks if the VM service can create VMs
func (s *Service) IsAvailable() bool {
	return verifyLibvirtAvailable() == nil
}

// CreateInstanceForChallenge creates a VM instance for a specific challenge
// This is a simplified wrapper for the instance handler
func (s *Service) CreateInstanceForChallenge(ctx context.Context, challengeID, instanceID string, templateID string) (*VMInstanceInfo, error) {
	// Look up the template by ID from in-memory cache
	s.mu.RLock()
	template, exists := s.templates[templateID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no VM template found with ID %s for challenge %s (template not loaded in memory)", templateID, challengeID)
	}

	req := CreateVMRequest{
		TemplateID:  template.ID,
		ChallengeID: challengeID,
		UserID:      "", // Will be set from context
		VCPU:        template.VCPU,
		MemoryMB:    template.MemoryMB,
	}

	instance, err := s.CreateInstance(ctx, req)
	if err != nil {
		return nil, err
	}

	return &VMInstanceInfo{
		VMID:      instance.ID,
		IPAddress: instance.IPAddress,
		VNCPort:   instance.VNCPort,
	}, nil
}

// CreateInstanceWithTemplate creates a VM instance using template data provided by caller
// This allows the caller to fetch template from database
func (s *Service) CreateInstanceWithTemplate(ctx context.Context, challengeID, instanceID string, template *VMTemplate) (*VMInstanceInfo, error) {
	if template == nil {
		return nil, fmt.Errorf("template cannot be nil")
	}

	// Cache the template for future use
	s.mu.Lock()
	s.templates[template.ID] = template
	s.mu.Unlock()

	req := CreateVMRequest{
		TemplateID:  template.ID,
		ChallengeID: challengeID,
		UserID:      "", // Will be set from context
		VCPU:        template.VCPU,
		MemoryMB:    template.MemoryMB,
	}

	instance, err := s.CreateInstance(ctx, req)
	if err != nil {
		return nil, err
	}

	return &VMInstanceInfo{
		VMID:      instance.ID,
		IPAddress: instance.IPAddress,
		VNCPort:   instance.VNCPort,
	}, nil
}

// VMInstanceInfo contains basic info returned to the instance handler
type VMInstanceInfo struct {
	VMID      string
	IPAddress string
	VNCPort   int
	NodeID    string
}

// CreateInstanceOnNode creates a VM instance on a specific node via SSH
func (s *Service) CreateInstanceOnNode(ctx context.Context, challengeID, instanceID string, template *VMTemplate, node *NodeInfo) (*VMInstanceInfo, error) {
	if template == nil {
		return nil, fmt.Errorf("template cannot be nil")
	}
	if node == nil {
		return nil, fmt.Errorf("node cannot be nil")
	}

	s.logger.Info("creating VM instance on node",
		zap.String("instance_id", instanceID),
		zap.String("template_id", template.ID),
		zap.String("node", node.Name),
		zap.String("node_ip", node.IPAddress),
	)

	// Create overlay disk on the remote node via SSH
	overlayPath, err := s.createOverlayOnNode(ctx, template.ImagePath, instanceID, node)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk overlay: %w", err)
	}

	// Allocate resources
	vncPort := s.allocateVNCPort()
	macAddress := generateMAC(instanceID)
	vmName := fmt.Sprintf("anvil-%s", instanceID[:8])

	// Generate libvirt XML
	domainXML, err := s.generateDomainXML(vmName, instanceID, template.VCPU, template.MemoryMB, overlayPath, macAddress, vncPort, node.NetworkName)
	if err != nil {
		return nil, fmt.Errorf("failed to generate domain XML: %w", err)
	}

	// Define and start VM on node via SSH + virsh
	err = s.defineAndStartVMOnNode(ctx, domainXML, vmName, node)
	if err != nil {
		// Cleanup overlay on failure
		s.runSSHCommand(ctx, node, fmt.Sprintf("rm -f %s", overlayPath))
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	// Wait for VM to get IP via DHCP (increased timeout to 60 seconds)
	// VMs rely entirely on dynamic DHCP allocation
	actualIP, err := s.queryVMIP(ctx, vmName, node, 60)
	if err != nil {
		// Failed to get IP - cleanup and fail
		s.logger.Error("VM failed to get IP address",
			zap.Error(err),
			zap.String("vm_name", vmName))
		virshCmd := "virsh -c qemu:///system"
		s.runSSHCommand(ctx, node, fmt.Sprintf("%s destroy %s 2>/dev/null || true", virshCmd, vmName))
		s.runSSHCommand(ctx, node, fmt.Sprintf("%s undefine %s 2>/dev/null || true", virshCmd, vmName))
		s.runSSHCommand(ctx, node, fmt.Sprintf("rm -f %s", overlayPath))
		return nil, fmt.Errorf("VM failed to get IP address after 60 seconds: %w", err)
	}

	s.logger.Info("VM acquired IP via DHCP",
		zap.String("instance_id", instanceID),
		zap.String("ip_address", actualIP))

	// Store instance info with actual IP
	instance := &VMInstance{
		ID:          instanceID,
		Name:        vmName,
		TemplateID:  template.ID,
		ChallengeID: challengeID,
		State:       VMStateRunning,
		VCPU:        template.VCPU,
		MemoryMB:    template.MemoryMB,
		DiskPath:    overlayPath,
		NetworkID:   node.NetworkName,
		IPAddress:   actualIP,
		MACAddress:  macAddress,
		VNCPort:     vncPort,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(s.config.DefaultDuration),
	}

	s.mu.Lock()
	s.instances[instanceID] = instance
	s.mu.Unlock()

	return &VMInstanceInfo{
		VMID:      instanceID,
		IPAddress: actualIP,
		VNCPort:   vncPort,
		NodeID:    node.ID,
	}, nil
}

// createOverlayOnNode creates a CoW overlay disk on a remote node via SSH
func (s *Service) createOverlayOnNode(ctx context.Context, basePath string, instanceID string, node *NodeInfo) (string, error) {
	overlayDir := "/var/lib/anvil/storage/vms/overlays"
	overlayPath := fmt.Sprintf("%s/%s.qcow2", overlayDir, instanceID)

	// Ensure overlay directory exists and create the overlay
	cmd := fmt.Sprintf("mkdir -p %s && qemu-img create -f qcow2 -F qcow2 -b %s %s",
		overlayDir, basePath, overlayPath)

	output, err := s.runSSHCommand(ctx, node, cmd)
	if err != nil {
		return "", fmt.Errorf("failed to create overlay: %s: %w", output, err)
	}

	return overlayPath, nil
}

// allocateIPWithReservation allocates a specific IP and creates a DHCP reservation
// This ensures the VM gets a predictable IP that we know before it boots
func (s *Service) allocateIPWithReservation(ctx context.Context, node *NodeInfo, macAddress, instanceID string) (string, error) {
	// Allocate next available IP from our pool
	s.mu.Lock()
	ipAddress := s.allocateIPLocked()
	s.mu.Unlock()

	// Add DHCP host reservation to libvirt network
	// Format: virsh net-update <network> add ip-dhcp-host "<host mac='XX:XX:XX:XX:XX:XX' ip='10.100.X.Y'/>" --live --config
	virshCmd := "virsh -c qemu:///system"
	hostXML := fmt.Sprintf("<host mac='%s' ip='%s'/>", macAddress, ipAddress)
	cmd := fmt.Sprintf("%s net-update %s add ip-dhcp-host \"%s\" --live --config",
		virshCmd, node.NetworkName, hostXML)

	output, err := s.runSSHCommand(ctx, node, cmd)
	if err != nil {
		s.logger.Warn("failed to add DHCP reservation, VM will use dynamic IP",
			zap.Error(err),
			zap.String("output", output),
			zap.String("mac", macAddress),
			zap.String("ip", ipAddress),
		)
		// Don't fail - VM will still get an IP from DHCP pool, just not this specific one
		// The IP shown in UI might differ from actual, but VM will work
	}

	s.logger.Info("allocated IP with DHCP reservation",
		zap.String("instance_id", instanceID),
		zap.String("mac", macAddress),
		zap.String("ip", ipAddress),
	)

	return ipAddress, nil
}

// removeIPReservation removes a DHCP reservation (cleanup on failure)
func (s *Service) removeIPReservation(ctx context.Context, node *NodeInfo, macAddress string) {
	virshCmd := "virsh -c qemu:///system"
	// We need to find the IP for this MAC to remove it properly
	// For simplicity, we'll just log the attempt - the reservation will be orphaned but harmless
	cmd := fmt.Sprintf("%s net-update %s delete ip-dhcp-host \"<host mac='%s'/>\" --live --config 2>/dev/null || true",
		virshCmd, node.NetworkName, macAddress)
	s.runSSHCommand(ctx, node, cmd)
}

// allocateIPLocked allocates the next available IP (must hold s.mu)
func (s *Service) allocateIPLocked() string {
	// Use 10.100.10.x - 10.100.250.x range (avoiding .0, .1, .255)
	// This gives us ~61,000 possible IPs
	for subnet := 10; subnet <= 250; subnet++ {
		for host := 10; host <= 250; host++ {
			ip := fmt.Sprintf("10.100.%d.%d", subnet, host)
			if !s.usedIPs[ip] {
				s.usedIPs[ip] = true
				return ip
			}
		}
	}
	// Fallback - should never happen with 61k IPs
	return fmt.Sprintf("10.100.%d.%d", 100+len(s.usedIPs)%150, 10+len(s.usedIPs)%240)
}

// defineAndStartVMOnNode defines and starts a VM on a remote node via SSH
func (s *Service) defineAndStartVMOnNode(ctx context.Context, domainXML, vmName string, node *NodeInfo) error {
	// Write XML to temp file on node, define VM, then start it
	xmlPath := fmt.Sprintf("/tmp/anvil-vm-%s.xml", vmName)

	// Escape the XML for shell
	escapedXML := strings.ReplaceAll(domainXML, "'", "'\\''")

	// Use virsh with explicit system connection URI
	// This is needed because SSH user session defaults to qemu:///session
	virshCmd := "virsh -c qemu:///system"

	// Write XML, define, start, cleanup
	cmd := fmt.Sprintf("echo '%s' > %s && %s define %s && %s start %s && rm -f %s",
		escapedXML, xmlPath, virshCmd, xmlPath, virshCmd, vmName, xmlPath)

	output, err := s.runSSHCommand(ctx, node, cmd)
	if err != nil {
		return fmt.Errorf("virsh command failed: %s: %w", output, err)
	}

	return nil
}

// runSSHCommand executes a command on a remote node via SSH
func (s *Service) runSSHCommand(ctx context.Context, node *NodeInfo, command string) (string, error) {
	sshArgs := []string{
		"-q", // Quiet mode - suppress warnings
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", // Only show errors, not warnings
		"-o", "ConnectTimeout=10",
		"-p", fmt.Sprintf("%d", node.SSHPort),
	}

	// Add SSH key if specified
	if node.SSHKeyPath != "" {
		sshArgs = append(sshArgs, "-i", node.SSHKeyPath)
	}

	target := fmt.Sprintf("%s@%s", node.SSHUser, node.IPAddress)
	sshArgs = append(sshArgs, target, command)

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	output, err := cmd.Output() // Use Output() instead of CombinedOutput() to only get stdout
	return string(output), err
}

// queryVMIP queries the actual IP address of a VM after boot
// Retries for up to timeoutSeconds to wait for DHCP
func (s *Service) queryVMIP(ctx context.Context, vmName string, node *NodeInfo, timeoutSeconds int) (string, error) {
	virshCmd := "virsh -c qemu:///system"
	cmd := fmt.Sprintf("%s domifaddr %s | grep -oP 'ipv4\\s+\\K[0-9.]+' | head -1", virshCmd, vmName)

	// Retry for up to timeout seconds
	for i := 0; i < timeoutSeconds; i++ {
		output, err := s.runSSHCommand(ctx, node, cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			ip := strings.TrimSpace(output)
			// Remove /prefix if present
			if idx := strings.Index(ip, "/"); idx > 0 {
				ip = ip[:idx]
			}
			return ip, nil
		}
		time.Sleep(1 * time.Second)
	}

	return "", fmt.Errorf("timeout waiting for VM to get IP address")
}

// generateDomainXML creates libvirt domain XML for a VM
func (s *Service) generateDomainXML(name, uuid string, vcpu, memoryMB int, diskPath, macAddress string, vncPort int, networkName string) (string, error) {
	xml := fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <uuid>%s</uuid>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <clock offset='utc'/>
  <on_poweroff>destroy</on_poweroff>
  <on_reboot>restart</on_reboot>
  <on_crash>destroy</on_crash>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <mac address='%s'/>
      <source network='%s'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='%d' autoport='no' listen='0.0.0.0'>
      <listen type='address' address='0.0.0.0'/>
    </graphics>
    <video>
      <model type='virtio'/>
    </video>
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>`, name, uuid, memoryMB, vcpu, diskPath, macAddress, networkName, vncPort)

	return xml, nil
}

// RegisterTemplate registers a new VM template from an uploaded image
func (s *Service) RegisterTemplate(ctx context.Context, template *VMTemplate) error {
	// Convert image to QCOW2 if needed (QCOW2 supports CoW snapshots)
	if template.ImageFormat != ImageFormatQCOW2 {
		qcow2Path, err := s.convertToQCOW2(ctx, template.ImagePath, template.ImageFormat)
		if err != nil {
			return fmt.Errorf("failed to convert image: %w", err)
		}
		template.ImagePath = qcow2Path
		template.ImageFormat = ImageFormatQCOW2
	}

	s.mu.Lock()
	s.templates[template.ID] = template
	s.mu.Unlock()

	s.logger.Info("VM template registered",
		zap.String("template_id", template.ID),
		zap.String("name", template.Name),
		zap.String("format", string(template.ImageFormat)),
	)

	return nil
}

// GetTemplate retrieves a template by ID
func (s *Service) GetTemplate(ctx context.Context, templateID string) (*VMTemplate, error) {
	s.mu.RLock()
	template, exists := s.templates[templateID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("template not found: %s", templateID)
	}

	return template, nil
}

// ListTemplates returns all available templates
func (s *Service) ListTemplates(ctx context.Context) ([]*VMTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	templates := make([]*VMTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		templates = append(templates, t)
	}

	return templates, nil
}

// CreateInstance creates a new VM instance from a template
func (s *Service) CreateInstance(ctx context.Context, req CreateVMRequest) (*VMInstance, error) {
	// Get template
	template, err := s.GetTemplate(ctx, req.TemplateID)
	if err != nil {
		return nil, err
	}

	// Check user limits
	userInstances := s.getUserInstanceCount(req.UserID)
	if userInstances >= s.config.MaxInstancesPerUser {
		return nil, fmt.Errorf("user has reached maximum instances limit (%d)", s.config.MaxInstancesPerUser)
	}

	// Use template defaults or overrides
	vcpu := template.VCPU
	if req.VCPU > 0 {
		vcpu = req.VCPU
	}
	memoryMB := template.MemoryMB
	if req.MemoryMB > 0 {
		memoryMB = req.MemoryMB
	}
	duration := s.config.DefaultDuration
	if req.Duration > 0 {
		duration = req.Duration
		if duration > s.config.MaxDuration {
			duration = s.config.MaxDuration
		}
	}

	instanceID := uuid.New().String()

	// Create CoW overlay disk
	overlayPath, err := s.createOverlay(ctx, template.ImagePath, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk overlay: %w", err)
	}

	// Allocate VNC port
	vncPort := s.allocateVNCPort()

	// Allocate IP address
	ipAddress := s.allocateIP()

	// Generate MAC address
	macAddress := generateMAC(instanceID)

	instance := &VMInstance{
		ID:           instanceID,
		Name:         fmt.Sprintf("anvil-%s", instanceID[:8]),
		TemplateID:   req.TemplateID,
		ChallengeID:  req.ChallengeID,
		UserID:       req.UserID,
		State:        VMStateProvisioning,
		VCPU:         vcpu,
		MemoryMB:     memoryMB,
		DiskPath:     overlayPath,
		IPAddress:    ipAddress,
		MACAddress:   macAddress,
		VNCPort:      vncPort,
		ExposedPorts: make(map[int]int),
		Metadata:     req.Metadata,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(duration),
	}

	// Define and start VM
	if err := s.defineAndStartVM(ctx, instance); err != nil {
		// Cleanup on failure
		os.Remove(overlayPath)
		s.releaseVNCPort(vncPort)
		s.releaseIP(ipAddress)
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	s.mu.Lock()
	s.instances[instanceID] = instance
	s.mu.Unlock()

	now := time.Now()
	instance.State = VMStateRunning
	instance.StartedAt = &now

	s.logger.Info("VM instance created",
		zap.String("instance_id", instanceID),
		zap.String("template_id", req.TemplateID),
		zap.String("user_id", req.UserID),
		zap.String("ip", ipAddress),
		zap.Int("vnc_port", vncPort),
	)

	return instance, nil
}

// GetInstance retrieves an instance by ID
func (s *Service) GetInstance(ctx context.Context, instanceID string) (*VMInstance, error) {
	s.mu.RLock()
	instance, exists := s.instances[instanceID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	// Update state from libvirt
	state, err := s.getVMState(ctx, instance.Name)
	if err == nil {
		instance.State = state
	}

	return instance, nil
}

// StopInstance stops a running VM instance
func (s *Service) StopInstance(ctx context.Context, instanceID string) error {
	instance, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	if err := s.stopVM(ctx, instance.Name); err != nil {
		return fmt.Errorf("failed to stop VM: %w", err)
	}

	s.mu.Lock()
	instance.State = VMStateShutoff
	s.mu.Unlock()

	s.logger.Info("VM instance stopped", zap.String("instance_id", instanceID))

	return nil
}

// StartInstance starts a stopped VM instance
func (s *Service) StartInstance(ctx context.Context, instanceID string) error {
	instance, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	if instance.State == VMStateRunning {
		return nil // Already running
	}

	if err := s.startVM(ctx, instance.Name); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}

	s.mu.Lock()
	now := time.Now()
	instance.State = VMStateRunning
	instance.StartedAt = &now
	s.mu.Unlock()

	s.logger.Info("VM instance started", zap.String("instance_id", instanceID))

	return nil
}

// ResetInstance resets a VM to its initial state
func (s *Service) ResetInstance(ctx context.Context, instanceID string) error {
	instance, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	template, err := s.GetTemplate(ctx, instance.TemplateID)
	if err != nil {
		return err
	}

	// Stop VM
	s.stopVM(ctx, instance.Name)

	// Delete old overlay
	os.Remove(instance.DiskPath)

	// Create new overlay
	overlayPath, err := s.createOverlay(ctx, template.ImagePath, instanceID)
	if err != nil {
		return fmt.Errorf("failed to create new overlay: %w", err)
	}

	s.mu.Lock()
	instance.DiskPath = overlayPath
	s.mu.Unlock()

	// Start VM with new disk
	if err := s.startVM(ctx, instance.Name); err != nil {
		return fmt.Errorf("failed to start VM after reset: %w", err)
	}

	s.mu.Lock()
	now := time.Now()
	instance.State = VMStateRunning
	instance.StartedAt = &now
	s.mu.Unlock()

	s.logger.Info("VM instance reset", zap.String("instance_id", instanceID))

	return nil
}

// DestroyInstance permanently destroys a VM instance
func (s *Service) DestroyInstance(ctx context.Context, instanceID string) error {
	instance, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	// Get node info for cleanup (use default for now)
	node := &NodeInfo{
		ID:          "default",
		Hostname:    "172.17.0.1",
		IPAddress:   "172.17.0.1",
		SSHPort:     22,
		SSHUser:     "root",
		SSHKeyPath:  "/root/.ssh/id_rsa",
		LibvirtURI:  s.config.LibvirtURI,
		NetworkName: s.config.NetworkName,
	}

	// Stop and undefine VM
	s.stopVM(ctx, instance.Name)
	s.undefineVM(ctx, instance.Name)

	// Cleanup resources (DHCP lease will expire automatically)
	os.Remove(instance.DiskPath)
	s.releaseVNCPort(instance.VNCPort)
	s.releaseIP(instance.IPAddress)

	s.mu.Lock()
	delete(s.instances, instanceID)
	s.mu.Unlock()

	s.logger.Info("VM instance destroyed", zap.String("instance_id", instanceID))

	return nil
}

// ListUserInstances returns all instances for a user
func (s *Service) ListUserInstances(ctx context.Context, userID string) ([]*VMInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var instances []*VMInstance
	for _, inst := range s.instances {
		if inst.UserID == userID {
			instances = append(instances, inst)
		}
	}

	return instances, nil
}

// CleanupExpired destroys expired instances
func (s *Service) CleanupExpired(ctx context.Context) error {
	s.mu.RLock()
	var expired []string
	now := time.Now()
	for id, inst := range s.instances {
		if now.After(inst.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	s.mu.RUnlock()

	for _, id := range expired {
		if err := s.DestroyInstance(ctx, id); err != nil {
			s.logger.Error("failed to destroy expired instance",
				zap.String("instance_id", id),
				zap.Error(err),
			)
		}
	}

	return nil
}

// ReconcileState cleans up orphaned VMs that aren't in our instance map
// This is useful on startup to clean up VMs left from previous crashes
func (s *Service) ReconcileState(ctx context.Context, nodeHostname, nodeIP, sshUser, sshKeyPath string) error {
	if nodeIP == "" {
		return fmt.Errorf("no node IP provided")
	}

	node := &NodeInfo{
		ID:          "default",
		Name:        "default",
		Hostname:    nodeHostname,
		IPAddress:   nodeIP,
		SSHPort:     22,
		SSHUser:     sshUser,
		SSHKeyPath:  sshKeyPath,
		LibvirtURI:  s.config.LibvirtURI,
		NetworkName: s.config.NetworkName,
	}

	// List all VMs with "anvil-" prefix
	virshCmd := "virsh -c qemu:///system"
	cmd := fmt.Sprintf("%s list --all --name | grep '^anvil-'", virshCmd)
	output, err := s.runSSHCommand(ctx, node, cmd)
	if err != nil {
		s.logger.Warn("failed to list VMs for reconciliation", zap.Error(err))
		return nil // Don't fail startup
	}

	vmNames := strings.Split(strings.TrimSpace(output), "\n")

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, vmName := range vmNames {
		vmName = strings.TrimSpace(vmName)
		if vmName == "" {
			continue
		}

		// Extract UUID from name (anvil-{8chars of UUID})
		// Check if we have this in our instances map
		found := false
		for _, inst := range s.instances {
			if inst.Name == vmName {
				found = true
				break
			}
		}

		if !found {
			// Orphaned VM - destroy it
			s.logger.Info("cleaning up orphaned VM", zap.String("vm_name", vmName))
			destroyCmd := fmt.Sprintf("%s destroy %s 2>/dev/null || true", virshCmd, vmName)
			s.runSSHCommand(ctx, node, destroyCmd)

			undefineCmd := fmt.Sprintf("%s undefine %s 2>/dev/null || true", virshCmd, vmName)
			s.runSSHCommand(ctx, node, undefineCmd)
		}
	}

	// Clean up stale DHCP reservations
	s.logger.Info("cleaning up stale DHCP reservations")
	cleanupCmd := fmt.Sprintf("%s net-dumpxml %s 2>/dev/null | grep -oP \"mac='\\K[^']+\" | while read mac; do %s net-update %s delete ip-dhcp-host \"<host mac='$mac'/>\" --live --config 2>/dev/null || true; done",
		virshCmd, node.NetworkName, virshCmd, node.NetworkName)
	s.runSSHCommand(ctx, node, cleanupCmd)

	return nil
}

// ExtendInstance extends the expiration time of an instance
func (s *Service) ExtendInstance(ctx context.Context, instanceID string, duration time.Duration) error {
	s.mu.Lock()
	instance, exists := s.instances[instanceID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	newExpiry := instance.ExpiresAt.Add(duration)
	maxExpiry := instance.CreatedAt.Add(s.config.MaxDuration)
	if newExpiry.After(maxExpiry) {
		newExpiry = maxExpiry
	}

	instance.ExpiresAt = newExpiry
	s.mu.Unlock()

	s.logger.Info("VM instance extended",
		zap.String("instance_id", instanceID),
		zap.Time("expires_at", newExpiry),
	)

	return nil
}

// convertToQCOW2 converts an image to QCOW2 format
func (s *Service) convertToQCOW2(ctx context.Context, imagePath string, format ImageFormat) (string, error) {
	outputPath := strings.TrimSuffix(imagePath, filepath.Ext(imagePath)) + ".qcow2"

	var inputFormat string
	switch format {
	case ImageFormatOVA:
		// OVA is a tar containing VMDK - extract first
		extractedPath, err := s.extractOVA(ctx, imagePath)
		if err != nil {
			return "", err
		}
		imagePath = extractedPath
		inputFormat = "vmdk"
	case ImageFormatVMDK:
		inputFormat = "vmdk"
	case ImageFormatVDI:
		inputFormat = "vdi"
	case ImageFormatRAW:
		inputFormat = "raw"
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}

	// Use qemu-img to convert
	cmd := exec.CommandContext(ctx, "qemu-img", "convert",
		"-f", inputFormat,
		"-O", "qcow2",
		"-o", "lazy_refcounts=on",
		imagePath,
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("qemu-img convert failed: %s: %w", string(output), err)
	}

	s.logger.Info("image converted to QCOW2",
		zap.String("input", imagePath),
		zap.String("output", outputPath),
	)

	return outputPath, nil
}

// extractOVA extracts VMDK from OVA file
func (s *Service) extractOVA(ctx context.Context, ovaPath string) (string, error) {
	extractDir := filepath.Join(s.config.ImageStorePath, "extracted", filepath.Base(ovaPath))
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	// OVA is just a tar file
	cmd := exec.CommandContext(ctx, "tar", "-xvf", ovaPath, "-C", extractDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to extract OVA: %s: %w", string(output), err)
	}

	// Find the VMDK file
	var vmdkPath string
	filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(strings.ToLower(path), ".vmdk") {
			vmdkPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if vmdkPath == "" {
		return "", fmt.Errorf("no VMDK found in OVA")
	}

	return vmdkPath, nil
}

// createOverlay creates a CoW overlay disk for an instance
func (s *Service) createOverlay(ctx context.Context, basePath string, instanceID string) (string, error) {
	overlayPath := filepath.Join(s.config.InstanceStorePath, "overlays", instanceID+".qcow2")

	cmd := exec.CommandContext(ctx, "qemu-img", "create",
		"-f", "qcow2",
		"-F", "qcow2",
		"-b", basePath,
		overlayPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create overlay: %s: %w", string(output), err)
	}

	return overlayPath, nil
}

// libvirt XML template for VM definition
const domainXMLTemplate = `
<domain type='kvm'>
  <name>{{.Name}}</name>
  <uuid>{{.ID}}</uuid>
  <memory unit='MiB'>{{.MemoryMB}}</memory>
  <vcpu>{{.VCPU}}</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <clock offset='utc'/>
  <on_poweroff>destroy</on_poweroff>
  <on_reboot>restart</on_reboot>
  <on_crash>destroy</on_crash>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='{{.DiskPath}}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <mac address='{{.MACAddress}}'/>
      <source network='{{.NetworkName}}'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='{{.VNCPort}}' listen='127.0.0.1'/>
    <video>
      <model type='virtio'/>
    </video>
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>
`

// VMDomainXML represents the XML structure for libvirt domain
type VMDomainXML struct {
	XMLName     xml.Name `xml:"domain"`
	Name        string
	ID          string
	MemoryMB    int
	VCPU        int
	DiskPath    string
	MACAddress  string
	NetworkName string
	VNCPort     int
}

// defineAndStartVM defines and starts a VM using virsh
func (s *Service) defineAndStartVM(ctx context.Context, instance *VMInstance) error {
	// For now, use virsh commands directly
	// In production, use libvirt Go bindings

	// Generate domain XML
	xmlPath := filepath.Join(s.config.InstanceStorePath, instance.ID+".xml")
	domainXML := fmt.Sprintf(`
<domain type='kvm'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <mac address='%s'/>
      <source network='%s'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='%d' listen='127.0.0.1'/>
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>`,
		instance.Name,
		instance.MemoryMB,
		instance.VCPU,
		instance.DiskPath,
		instance.MACAddress,
		s.config.NetworkName,
		instance.VNCPort,
	)

	if err := os.WriteFile(xmlPath, []byte(domainXML), 0644); err != nil {
		return fmt.Errorf("failed to write domain XML: %w", err)
	}
	defer os.Remove(xmlPath)

	// Define domain
	cmd := exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "define", xmlPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("virsh define failed: %s: %w", string(output), err)
	}

	// Start domain
	cmd = exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "start", instance.Name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("virsh start failed: %s: %w", string(output), err)
	}

	return nil
}

func (s *Service) stopVM(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "destroy", name)
	cmd.CombinedOutput() // Ignore errors if VM is already stopped
	return nil
}

func (s *Service) startVM(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "start", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("virsh start failed: %s: %w", string(output), err)
	}
	return nil
}

func (s *Service) undefineVM(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "undefine", name)
	cmd.CombinedOutput() // Ignore errors
	return nil
}

func (s *Service) getVMState(ctx context.Context, name string) (VMState, error) {
	cmd := exec.CommandContext(ctx, "virsh", "-c", s.config.LibvirtURI, "domstate", name)
	output, err := cmd.Output()
	if err != nil {
		return VMStateNoState, err
	}

	state := strings.TrimSpace(string(output))
	switch state {
	case "running":
		return VMStateRunning, nil
	case "paused":
		return VMStatePaused, nil
	case "shut off":
		return VMStateShutoff, nil
	case "crashed":
		return VMStateCrashed, nil
	default:
		return VMStateNoState, nil
	}
}

// Helper functions

func (s *Service) getUserInstanceCount(userID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, inst := range s.instances {
		if inst.UserID == userID {
			count++
		}
	}
	return count
}

func (s *Service) allocateVNCPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	for port := s.config.VNCPortStart; port <= s.config.VNCPortEnd; port++ {
		if !s.usedVNCPorts[port] {
			s.usedVNCPorts[port] = true
			return port
		}
	}
	return 0 // No available ports
}

func (s *Service) releaseVNCPort(port int) {
	s.mu.Lock()
	delete(s.usedVNCPorts, port)
	s.mu.Unlock()
}

func (s *Service) allocateIP() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simple sequential IP allocation from 10.100.1.1
	// In production, use proper IPAM
	for i := 1; i < 65534; i++ {
		ip := fmt.Sprintf("10.100.%d.%d", i/254+1, i%254+1)
		if !s.usedIPs[ip] {
			s.usedIPs[ip] = true
			return ip
		}
	}
	return ""
}

func (s *Service) releaseIP(ip string) {
	s.mu.Lock()
	delete(s.usedIPs, ip)
	s.mu.Unlock()
}

func generateMAC(seed string) string {
	// Generate deterministic MAC from seed
	// Using 52:54:00 prefix (QEMU default)
	hash := []byte(seed)
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", hash[0], hash[1], hash[2])
}

func verifyLibvirtAvailable() error {
	cmd := exec.Command("virsh", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("virsh not available: %w", err)
	}
	return nil
}
