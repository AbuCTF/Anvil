-- 003_vm_nodes_and_timers.sql
-- Adds support for multi-node VM orchestration, challenge-specific timers, and cooldown periods

-- ============================================================================
-- VM NODES (Worker nodes that can run VMs)
-- ============================================================================

CREATE TYPE node_status AS ENUM (
    'online',
    'offline',
    'maintenance',
    'draining'  -- No new VMs, waiting for existing to finish
);

CREATE TABLE vm_nodes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    
    -- Node identification
    name VARCHAR(100) NOT NULL UNIQUE,
    hostname VARCHAR(255) NOT NULL,
    ip_address VARCHAR(45) NOT NULL,
    
    -- Connection details
    ssh_port INTEGER DEFAULT 22,
    ssh_user VARCHAR(50) DEFAULT 'anvil',
    ssh_key_path VARCHAR(500),
    libvirt_uri VARCHAR(255) DEFAULT 'qemu:///system',
    
    -- API connection (if running agent)
    api_endpoint VARCHAR(255),  -- e.g., http://node1:8081
    api_key_hash VARCHAR(255),
    
    -- Capacity
    total_vcpu INTEGER NOT NULL,
    total_memory_mb INTEGER NOT NULL,
    total_disk_gb INTEGER NOT NULL,
    
    -- Current usage (updated by health checks)
    used_vcpu INTEGER DEFAULT 0,
    used_memory_mb INTEGER DEFAULT 0,
    used_disk_gb INTEGER DEFAULT 0,
    active_vms INTEGER DEFAULT 0,
    
    -- Limits
    max_vms INTEGER DEFAULT 10,
    reserved_vcpu INTEGER DEFAULT 2,      -- Keep for host OS
    reserved_memory_mb INTEGER DEFAULT 4096,
    
    -- Network config for this node
    vm_network_name VARCHAR(100) DEFAULT 'anvil-lab',
    vm_subnet VARCHAR(50),  -- e.g., 10.10.1.0/24 for node 1
    
    -- VNC port range for this node
    vnc_port_start INTEGER DEFAULT 5900,
    vnc_port_end INTEGER DEFAULT 6100,
    
    -- Status
    status node_status DEFAULT 'offline',
    last_heartbeat TIMESTAMPTZ,
    last_health_check TIMESTAMPTZ,
    health_check_error TEXT,
    
    -- Is this the primary/orchestrator node?
    is_primary BOOLEAN DEFAULT FALSE,
    
    -- Node priority for scheduling (higher = preferred)
    priority INTEGER DEFAULT 0,
    
    -- Metadata
    region VARCHAR(50),  -- e.g., us-central1
    zone VARCHAR(50),    -- e.g., us-central1-b
    provider VARCHAR(50), -- gcp, aws, local
    tags JSONB DEFAULT '[]',
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_vm_nodes_status ON vm_nodes(status);
CREATE INDEX idx_vm_nodes_priority ON vm_nodes(priority DESC);
CREATE INDEX idx_vm_nodes_region ON vm_nodes(region);

-- ============================================================================
-- CHALLENGE TIMER SETTINGS
-- ============================================================================

-- Add timer and cooldown columns to challenges table
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS vm_timeout_minutes INTEGER;
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS vm_max_extensions INTEGER DEFAULT 2;
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS vm_extension_minutes INTEGER DEFAULT 30;
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS cooldown_minutes INTEGER DEFAULT 10;

-- Add default timeout based on difficulty
COMMENT ON COLUMN challenges.vm_timeout_minutes IS 'VM instance timeout in minutes. If NULL, uses difficulty-based default: easy=60, medium=120, hard=180, insane=240';
COMMENT ON COLUMN challenges.vm_max_extensions IS 'Maximum number of time extensions allowed';
COMMENT ON COLUMN challenges.vm_extension_minutes IS 'Minutes added per extension';
COMMENT ON COLUMN challenges.cooldown_minutes IS 'Cooldown period in minutes after instance expires before user can start another';

-- ============================================================================
-- USER COOLDOWNS
-- ============================================================================

CREATE TABLE user_cooldowns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    
    -- Cooldown details
    cooldown_until TIMESTAMPTZ NOT NULL,
    reason VARCHAR(50) DEFAULT 'instance_expired', -- instance_expired, abuse, manual
    
    -- Instance that triggered the cooldown
    instance_id UUID,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(user_id, challenge_id)
);

CREATE INDEX idx_user_cooldowns_user ON user_cooldowns(user_id);
CREATE INDEX idx_user_cooldowns_until ON user_cooldowns(cooldown_until);

-- ============================================================================
-- UPDATE VM_INSTANCES TABLE
-- ============================================================================

-- Add node reference to vm_instances
ALTER TABLE vm_instances ADD COLUMN IF NOT EXISTS node_id UUID REFERENCES vm_nodes(id);
CREATE INDEX IF NOT EXISTS idx_vm_instances_node ON vm_instances(node_id);

-- Add cooldown tracking
ALTER TABLE vm_instances ADD COLUMN IF NOT EXISTS cooldown_applied BOOLEAN DEFAULT FALSE;

-- ============================================================================
-- NODE HEALTH HISTORY (for monitoring)
-- ============================================================================

CREATE TABLE node_health_history (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    node_id UUID NOT NULL REFERENCES vm_nodes(id) ON DELETE CASCADE,
    
    -- Snapshot of usage at check time
    used_vcpu INTEGER,
    used_memory_mb INTEGER,
    used_disk_gb INTEGER,
    active_vms INTEGER,
    
    -- System metrics
    load_average DECIMAL(5,2),
    cpu_percent DECIMAL(5,2),
    memory_percent DECIMAL(5,2),
    disk_percent DECIMAL(5,2),
    
    -- Network metrics
    network_rx_bytes BIGINT,
    network_tx_bytes BIGINT,
    
    -- Status at check time
    status node_status,
    is_healthy BOOLEAN,
    error_message TEXT,
    
    recorded_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_node_health_node ON node_health_history(node_id);
CREATE INDEX idx_node_health_time ON node_health_history(recorded_at);

-- Partition or auto-cleanup old health data (keep 7 days)
-- This could be done via pg_cron or application cleanup job

-- ============================================================================
-- PLATFORM SETTINGS FOR MULTI-NODE
-- ============================================================================

INSERT INTO platform_settings (key, value, description) VALUES
    ('vm_default_timeout_easy', '60', 'Default VM timeout in minutes for easy challenges'),
    ('vm_default_timeout_medium', '120', 'Default VM timeout in minutes for medium challenges'),
    ('vm_default_timeout_hard', '180', 'Default VM timeout in minutes for hard challenges'),
    ('vm_default_timeout_insane', '240', 'Default VM timeout in minutes for insane challenges'),
    ('vm_default_cooldown', '10', 'Default cooldown in minutes after VM expires'),
    ('vm_scheduling_algorithm', '"least-loaded"', 'VM scheduling algorithm: least-loaded, round-robin, random'),
    ('vm_node_health_interval', '30', 'Seconds between node health checks'),
    ('vm_node_heartbeat_timeout', '120', 'Seconds before node is marked offline if no heartbeat'),
    ('multi_node_enabled', 'false', 'Enable multi-node VM orchestration'),
    ('vm_image_sync_enabled', 'false', 'Enable automatic VM image sync to worker nodes')
ON CONFLICT (key) DO NOTHING;

-- ============================================================================
-- FUNCTIONS
-- ============================================================================

-- Function to get effective timeout for a challenge
CREATE OR REPLACE FUNCTION get_challenge_timeout_minutes(p_challenge_id UUID)
RETURNS INTEGER AS $$
DECLARE
    v_timeout INTEGER;
    v_difficulty challenge_difficulty;
    v_default INTEGER;
BEGIN
    -- Get challenge-specific timeout
    SELECT vm_timeout_minutes, difficulty INTO v_timeout, v_difficulty
    FROM challenges WHERE id = p_challenge_id;
    
    -- If set, return it
    IF v_timeout IS NOT NULL THEN
        RETURN v_timeout;
    END IF;
    
    -- Otherwise, get default based on difficulty
    SELECT value::INTEGER INTO v_default
    FROM platform_settings 
    WHERE key = 'vm_default_timeout_' || v_difficulty::TEXT;
    
    RETURN COALESCE(v_default, 120); -- Fallback to 2 hours
END;
$$ LANGUAGE plpgsql;

-- Function to check if user is on cooldown for a challenge
CREATE OR REPLACE FUNCTION is_user_on_cooldown(p_user_id UUID, p_challenge_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1 FROM user_cooldowns 
        WHERE user_id = p_user_id 
        AND challenge_id = p_challenge_id 
        AND cooldown_until > NOW()
    );
END;
$$ LANGUAGE plpgsql;

-- Function to get cooldown remaining seconds
CREATE OR REPLACE FUNCTION get_cooldown_remaining_seconds(p_user_id UUID, p_challenge_id UUID)
RETURNS INTEGER AS $$
DECLARE
    v_until TIMESTAMPTZ;
BEGIN
    SELECT cooldown_until INTO v_until
    FROM user_cooldowns 
    WHERE user_id = p_user_id AND challenge_id = p_challenge_id;
    
    IF v_until IS NULL OR v_until <= NOW() THEN
        RETURN 0;
    END IF;
    
    RETURN EXTRACT(EPOCH FROM (v_until - NOW()))::INTEGER;
END;
$$ LANGUAGE plpgsql;

-- Function to select best node for new VM
CREATE OR REPLACE FUNCTION select_vm_node(p_required_vcpu INTEGER, p_required_memory_mb INTEGER)
RETURNS UUID AS $$
DECLARE
    v_node_id UUID;
BEGIN
    -- Select node with most available resources (least-loaded algorithm)
    SELECT id INTO v_node_id
    FROM vm_nodes
    WHERE status = 'online'
      AND active_vms < max_vms
      AND (total_vcpu - used_vcpu - reserved_vcpu) >= p_required_vcpu
      AND (total_memory_mb - used_memory_mb - reserved_memory_mb) >= p_required_memory_mb
    ORDER BY 
        priority DESC,
        (total_vcpu - used_vcpu - reserved_vcpu) DESC,
        (total_memory_mb - used_memory_mb - reserved_memory_mb) DESC
    LIMIT 1;
    
    RETURN v_node_id;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- TRIGGERS
-- ============================================================================

CREATE TRIGGER update_vm_nodes_updated_at BEFORE UPDATE ON vm_nodes
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================================================
-- INSERT PRIMARY NODE (current server)
-- ============================================================================

-- This will be populated by the application on startup
-- INSERT INTO vm_nodes (name, hostname, ip_address, total_vcpu, total_memory_mb, total_disk_gb, is_primary, status)
-- VALUES ('nestu', 'nestu.internal', '10.128.0.x', 16, 61440, 100, TRUE, 'online');

