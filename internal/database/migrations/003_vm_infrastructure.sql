-- Migration 003: VM Infrastructure & VPN
-- This migration adds VM and VPN related tables for B2R challenges

-- VM Templates table (stores processed QCOW2 base images)
CREATE TABLE IF NOT EXISTS vm_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    image_path VARCHAR(512),
    image_size BIGINT DEFAULT 0,
    vcpu INTEGER DEFAULT 2,
    memory_mb INTEGER DEFAULT 2048,
    disk_size_gb INTEGER DEFAULT 0,
    os_type VARCHAR(50) DEFAULT 'linux',
    checksum VARCHAR(64),
    template_status VARCHAR(50) DEFAULT 'processing',
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- VM Instances table (tracks running/stopped VM instances)
CREATE TABLE IF NOT EXISTS vm_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID REFERENCES vm_templates(id),
    challenge_id UUID REFERENCES challenges(id),
    user_id UUID NOT NULL REFERENCES users(id),
    overlay_path VARCHAR(512),
    instance_status VARCHAR(50) DEFAULT 'creating',
    vm_ip VARCHAR(45),
    user_vpn_ip VARCHAR(45),
    subnet_cidr VARCHAR(45),
    vcpu INTEGER DEFAULT 2,
    memory_mb INTEGER DEFAULT 2048,
    libvirt_name VARCHAR(255),
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    error_message TEXT
);

-- Network allocations table (tracks /24 subnet assignments per user)
CREATE TABLE IF NOT EXISTS network_allocations (
    id SERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    cidr VARCHAR(45) NOT NULL,
    user_ip VARCHAR(45) NOT NULL,
    vm_ip VARCHAR(45) NOT NULL,
    gateway_ip VARCHAR(45) NOT NULL,
    bridge_name VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- OVA upload tracking (for resumable uploads)
CREATE TABLE IF NOT EXISTS ova_uploads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filename VARCHAR(255) NOT NULL,
    file_size BIGINT NOT NULL,
    uploaded_size BIGINT DEFAULT 0,
    upload_path VARCHAR(512) NOT NULL,
    upload_status VARCHAR(50) DEFAULT 'uploading',
    checksum VARCHAR(64),
    uploaded_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- VPN Peers table (WireGuard peer configurations)
CREATE TABLE IF NOT EXISTS vpn_peers (
    id SERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    public_key VARCHAR(64) NOT NULL,
    private_key_encrypted VARCHAR(256) NOT NULL,
    allowed_ips VARCHAR(45) NOT NULL,
    assigned_ip VARCHAR(45) NOT NULL,
    last_handshake TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT vpn_peers_user_unique UNIQUE (user_id)
);

-- VPN connection logs (for auditing)
CREATE TABLE IF NOT EXISTS vpn_connection_logs (
    id SERIAL PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    peer_ip VARCHAR(45),
    connected_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    disconnected_at TIMESTAMP WITH TIME ZONE,
    bytes_sent BIGINT DEFAULT 0,
    bytes_received BIGINT DEFAULT 0
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_vm_instances_user_id ON vm_instances(user_id);
CREATE INDEX IF NOT EXISTS idx_vm_instances_status ON vm_instances(instance_status);
CREATE INDEX IF NOT EXISTS idx_vm_instances_challenge_id ON vm_instances(challenge_id);
CREATE INDEX IF NOT EXISTS idx_vm_templates_status ON vm_templates(template_status);
CREATE INDEX IF NOT EXISTS idx_vpn_peers_user_id ON vpn_peers(user_id);

-- Add challenge_type to challenges if not exists
DO $$ 
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns 
                   WHERE table_name = 'challenges' AND column_name = 'challenge_type') THEN
        ALTER TABLE challenges ADD COLUMN challenge_type VARCHAR(20) DEFAULT 'docker';
    END IF;
END $$;

-- Add vm_template_id to challenges if not exists
DO $$ 
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns 
                   WHERE table_name = 'challenges' AND column_name = 'vm_template_id') THEN
        ALTER TABLE challenges ADD COLUMN vm_template_id UUID REFERENCES vm_templates(id);
    END IF;
END $$;
