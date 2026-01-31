#!/bin/bash
# Anvil VM Host Setup Script
# Run this on GCP instances with nested virtualization enabled
# Instance type: n2-standard-8 or higher recommended

set -e

echo "=== Anvil VM Host Setup ==="
echo "This script will install libvirt, QEMU, and WireGuard"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "Please run as root (sudo)"
    exit 1
fi

# Check for nested virtualization
VMX_COUNT=$(grep -cw vmx /proc/cpuinfo 2>/dev/null || echo "0")
SVM_COUNT=$(grep -cw svm /proc/cpuinfo 2>/dev/null || echo "0")

if [ "$VMX_COUNT" -eq 0 ] && [ "$SVM_COUNT" -eq 0 ]; then
    echo "ERROR: Nested virtualization is NOT enabled!"
    echo ""
    echo "To enable on GCP:"
    echo "1. Stop this instance"
    echo "2. Export the disk to a custom image with:"
    echo "   gcloud compute images create anvil-vm-host-image \\"
    echo "     --source-disk=DISK_NAME --source-disk-zone=ZONE \\"
    echo "     --licenses='https://www.googleapis.com/compute/v1/projects/vm-options/global/licenses/enable-vmx'"
    echo "3. Create a new instance from this image using N1/N2/N2D machine types"
    echo ""
    echo "Or create a new instance directly with nested virt:"
    echo "   gcloud compute instances create anvil-vm-host \\"
    echo "     --zone=us-central1-a \\"
    echo "     --machine-type=n2-standard-8 \\"
    echo "     --min-cpu-platform='Intel Haswell' \\"
    echo "     --enable-nested-virtualization \\"
    echo "     --image-family=ubuntu-2204-lts \\"
    echo "     --image-project=ubuntu-os-cloud \\"
    echo "     --boot-disk-size=100GB \\"
    echo "     --boot-disk-type=pd-ssd"
    exit 1
fi

echo "✓ Nested virtualization detected (VMX: $VMX_COUNT, SVM: $SVM_COUNT)"

# Update system
echo ""
echo "=== Updating system ==="
apt-get update
apt-get upgrade -y

# Install virtualization packages
echo ""
echo "=== Installing virtualization packages ==="
apt-get install -y \
    qemu-kvm \
    libvirt-daemon-system \
    libvirt-clients \
    virtinst \
    bridge-utils \
    cpu-checker \
    virt-manager \
    libguestfs-tools \
    cloud-image-utils

# Verify KVM
echo ""
echo "=== Verifying KVM ==="
kvm-ok || echo "Warning: KVM might not work properly"

# Enable and start libvirt
echo ""
echo "=== Starting libvirt ==="
systemctl enable libvirtd
systemctl start libvirtd

# Add current user to libvirt group
SUDO_USER_REAL=${SUDO_USER:-$USER}
usermod -aG libvirt "$SUDO_USER_REAL"
usermod -aG kvm "$SUDO_USER_REAL"

# Install WireGuard
echo ""
echo "=== Installing WireGuard ==="
apt-get install -y wireguard wireguard-tools

# Enable IP forwarding
echo ""
echo "=== Configuring IP forwarding ==="
cat > /etc/sysctl.d/99-anvil.conf << EOF
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
EOF
sysctl -p /etc/sysctl.d/99-anvil.conf

# Create WireGuard config directory
mkdir -p /etc/wireguard

# Create Anvil directories
echo ""
echo "=== Creating Anvil directories ==="
mkdir -p /var/lib/anvil/vms
mkdir -p /var/lib/anvil/images
mkdir -p /var/lib/anvil/temp
chown -R "$SUDO_USER_REAL:$SUDO_USER_REAL" /var/lib/anvil

# Create libvirt network for Anvil VMs
echo ""
echo "=== Creating Anvil VM network ==="
cat > /tmp/anvil-network.xml << EOF
<network>
  <name>anvil-vms</name>
  <forward mode='nat'>
    <nat>
      <port start='1024' end='65535'/>
    </nat>
  </forward>
  <bridge name='virbr-anvil' stp='on' delay='0'/>
  <ip address='10.20.0.1' netmask='255.255.0.0'>
    <dhcp>
      <range start='10.20.1.1' end='10.20.255.254'/>
    </dhcp>
  </ip>
</network>
EOF

virsh net-define /tmp/anvil-network.xml || echo "Network might already exist"
virsh net-start anvil-vms || echo "Network might already be running"
virsh net-autostart anvil-vms

# Install Docker (for API/Web containers)
echo ""
echo "=== Installing Docker ==="
curl -fsSL https://get.docker.com | sh
usermod -aG docker "$SUDO_USER_REAL"

# Install Docker Compose
apt-get install -y docker-compose-plugin

# Create WireGuard server config template
echo ""
echo "=== Creating WireGuard server config ==="
cat > /etc/wireguard/wg0.conf.template << 'EOF'
[Interface]
# Server private key - REPLACE THIS
PrivateKey = SERVER_PRIVATE_KEY
Address = 10.10.0.1/16
ListenPort = 51820
PostUp = iptables -A FORWARD -i %i -j ACCEPT; iptables -A FORWARD -o %i -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i %i -j ACCEPT; iptables -D FORWARD -o %i -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE
SaveConfig = false

# Peers will be added dynamically by Anvil
EOF

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Next steps:"
echo "1. Configure WireGuard:"
echo "   - Copy your server private key to /etc/wireguard/wg0.conf"
echo "   - Start with: systemctl enable --now wg-quick@wg0"
echo ""
echo "2. Open firewall ports:"
echo "   - 51820/udp (WireGuard)"
echo "   - 8080/tcp (API)"
echo "   - 3000/tcp (Web)"
echo ""
echo "3. Clone and run Anvil:"
echo "   git clone https://github.com/AbuCTF/Anvil.git"
echo "   cd Anvil"
echo "   docker compose up -d"
echo ""
echo "4. Log out and back in for group changes to take effect"
