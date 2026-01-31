-- 002_vm_and_uploads.sql
-- Adds support for VM-based challenges and file uploads

-- ============================================================================
-- RESOURCE TYPES
-- ============================================================================

-- Resource type enum (Docker containers vs VMs)
CREATE TYPE resource_type AS ENUM ('docker', 'vm');

-- Image format enum for VMs
CREATE TYPE vm_image_format AS ENUM ('ova', 'vmdk', 'qcow2', 'vdi', 'raw', 'iso');

-- Upload status enum
CREATE TYPE upload_status AS ENUM (
    'pending',
    'uploading', 
    'processing',
    'validating',
    'completed',
    'failed',
    'cancelled'
);

-- ============================================================================
-- FILE UPLOADS
-- ============================================================================

CREATE TABLE uploads (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id),
    challenge_id UUID REFERENCES challenges(id),
    
    -- File info
    filename VARCHAR(500) NOT NULL,
    file_type VARCHAR(50) NOT NULL, -- dockerfile, docker_context, docker_image, ova, vmdk, qcow2, etc.
    content_type VARCHAR(200),
    total_size BIGINT NOT NULL,
    uploaded_size BIGINT DEFAULT 0,
    
    -- Chunked upload info
    chunk_size INTEGER NOT NULL,
    total_chunks INTEGER NOT NULL,
    uploaded_chunks INTEGER DEFAULT 0,
    
    -- Storage info
    storage_key VARCHAR(500) NOT NULL,
    storage_backend VARCHAR(50) DEFAULT 'local', -- local, gcs, s3
    backend_upload_id VARCHAR(500), -- For multipart uploads
    
    -- Status
    status upload_status DEFAULT 'pending',
    error_message TEXT,
    
    -- Verification
    checksum_expected VARCHAR(100), -- SHA256 provided by uploader
    checksum_actual VARCHAR(100),   -- SHA256 calculated after upload
    
    -- Processing results
    processed_path VARCHAR(500),    -- Path to processed file (e.g., converted QCOW2)
    processing_log TEXT,
    
    -- Validation results
    validation_passed BOOLEAN,
    validation_results JSONB,       -- Detailed validation info
    
    -- Timing
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ -- For incomplete uploads
);

CREATE INDEX idx_uploads_user ON uploads(user_id);
CREATE INDEX idx_uploads_challenge ON uploads(challenge_id);
CREATE INDEX idx_uploads_status ON uploads(status);
CREATE INDEX idx_uploads_expires ON uploads(expires_at);
CREATE INDEX idx_uploads_storage_key ON uploads(storage_key);

-- Upload chunks tracking (for resumable uploads)
CREATE TABLE upload_chunks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    upload_id UUID NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    chunk_number INTEGER NOT NULL,
    size BIGINT NOT NULL,
    etag VARCHAR(100),
    uploaded_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(upload_id, chunk_number)
);

CREATE INDEX idx_upload_chunks_upload ON upload_chunks(upload_id);

-- ============================================================================
-- VM TEMPLATES
-- ============================================================================

CREATE TABLE vm_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    upload_id UUID REFERENCES uploads(id),
    
    -- Basic info
    name VARCHAR(200) NOT NULL,
    slug VARCHAR(200) UNIQUE NOT NULL,
    description TEXT,
    
    -- Image info
    image_path VARCHAR(500) NOT NULL,       -- Path to QCOW2 file
    original_format vm_image_format NOT NULL,
    original_path VARCHAR(500),              -- Original uploaded file
    image_size BIGINT NOT NULL,
    
    -- VM specifications
    vcpu INTEGER NOT NULL DEFAULT 2,
    memory_mb INTEGER NOT NULL DEFAULT 2048,
    disk_gb INTEGER NOT NULL,
    
    -- OS info (for display/compatibility)
    os_type VARCHAR(50),           -- linux, windows
    os_variant VARCHAR(100),       -- ubuntu20.04, windows10, etc.
    os_name VARCHAR(200),          -- "Ubuntu 20.04 LTS"
    
    -- Hardware requirements
    requires_kvm BOOLEAN DEFAULT TRUE,
    requires_nested_virt BOOLEAN DEFAULT FALSE,
    gpu_required BOOLEAN DEFAULT FALSE,
    
    -- Network config
    network_mode VARCHAR(50) DEFAULT 'nat', -- nat, bridge, isolated
    exposed_services JSONB DEFAULT '[]',    -- [{"name": "SSH", "port": 22}, {"name": "HTTP", "port": 80}]
    
    -- Author/ownership
    author_id UUID REFERENCES users(id),
    is_public BOOLEAN DEFAULT FALSE,
    
    -- Status
    is_active BOOLEAN DEFAULT TRUE,
    
    -- Metadata
    metadata JSONB DEFAULT '{}',
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_vm_templates_slug ON vm_templates(slug);
CREATE INDEX idx_vm_templates_author ON vm_templates(author_id);
CREATE INDEX idx_vm_templates_active ON vm_templates(is_active);
CREATE INDEX idx_vm_templates_os ON vm_templates(os_type, os_variant);

-- ============================================================================
-- CHALLENGE RESOURCES
-- ============================================================================

-- Link challenges to their resources (Docker images OR VM templates)
CREATE TABLE challenge_resources (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    
    -- Resource type and reference
    resource_type resource_type NOT NULL,
    
    -- For Docker resources
    docker_image VARCHAR(500),
    docker_registry VARCHAR(200),
    docker_tag VARCHAR(100) DEFAULT 'latest',
    dockerfile_upload_id UUID REFERENCES uploads(id),
    
    -- For VM resources  
    vm_template_id UUID REFERENCES vm_templates(id),
    
    -- Resource limits (can override challenge defaults)
    cpu_limit VARCHAR(20),
    memory_limit VARCHAR(20),
    
    -- Network config
    exposed_ports JSONB DEFAULT '[]',
    
    -- Priority (for challenges with multiple resources)
    sort_order INTEGER DEFAULT 0,
    
    -- Status
    is_active BOOLEAN DEFAULT TRUE,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    -- Ensure either docker or VM config is provided
    CONSTRAINT resource_has_config CHECK (
        (resource_type = 'docker' AND (docker_image IS NOT NULL OR dockerfile_upload_id IS NOT NULL)) OR
        (resource_type = 'vm' AND vm_template_id IS NOT NULL)
    )
);

CREATE INDEX idx_challenge_resources_challenge ON challenge_resources(challenge_id);
CREATE INDEX idx_challenge_resources_type ON challenge_resources(resource_type);
CREATE INDEX idx_challenge_resources_vm ON challenge_resources(vm_template_id);

-- ============================================================================
-- VM INSTANCES
-- ============================================================================

-- VM instance status
CREATE TYPE vm_instance_status AS ENUM (
    'provisioning',
    'starting',
    'running',
    'paused',
    'stopping',
    'stopped',
    'error',
    'expired',
    'destroyed'
);

CREATE TABLE vm_instances (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id),
    resource_id UUID NOT NULL REFERENCES challenge_resources(id),
    vm_template_id UUID NOT NULL REFERENCES vm_templates(id),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    
    -- Instance details
    name VARCHAR(200) NOT NULL,
    status vm_instance_status DEFAULT 'provisioning',
    
    -- Disk
    overlay_path VARCHAR(500), -- CoW overlay disk path
    
    -- Resources
    vcpu INTEGER NOT NULL,
    memory_mb INTEGER NOT NULL,
    
    -- Network
    network_id VARCHAR(100),
    ip_address VARCHAR(45),
    mac_address VARCHAR(20),
    assigned_ports JSONB DEFAULT '{}', -- {"22": 32022, "80": 32080}
    
    -- Access
    vnc_port INTEGER,
    vnc_password VARCHAR(50),
    ssh_port INTEGER,
    
    -- Timing
    started_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    stopped_at TIMESTAMPTZ,
    extensions_used INTEGER DEFAULT 0,
    
    -- Error tracking
    error_message TEXT,
    
    -- Host info (for multi-node setups)
    host_node VARCHAR(200),
    
    -- Metadata
    metadata JSONB DEFAULT '{}',
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT vm_instance_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

CREATE INDEX idx_vm_instances_challenge ON vm_instances(challenge_id);
CREATE INDEX idx_vm_instances_template ON vm_instances(vm_template_id);
CREATE INDEX idx_vm_instances_user ON vm_instances(user_id);
CREATE INDEX idx_vm_instances_status ON vm_instances(status);
CREATE INDEX idx_vm_instances_expires ON vm_instances(expires_at);
CREATE INDEX idx_vm_instances_host ON vm_instances(host_node);

-- ============================================================================
-- DOCKER BUILD QUEUE (for Dockerfile-based challenges)
-- ============================================================================

CREATE TYPE build_status AS ENUM (
    'queued',
    'building',
    'pushing',
    'completed',
    'failed',
    'cancelled'
);

CREATE TABLE docker_builds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID REFERENCES challenges(id),
    resource_id UUID REFERENCES challenge_resources(id),
    upload_id UUID NOT NULL REFERENCES uploads(id),
    
    -- Build info
    image_name VARCHAR(500) NOT NULL,
    image_tag VARCHAR(100) NOT NULL,
    
    -- Status
    status build_status DEFAULT 'queued',
    
    -- Build details
    build_args JSONB DEFAULT '{}',
    build_log TEXT,
    
    -- Results
    image_digest VARCHAR(100),
    image_size BIGINT,
    
    -- Timing
    queued_at TIMESTAMPTZ DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    
    -- Error
    error_message TEXT,
    
    -- Triggered by
    triggered_by UUID REFERENCES users(id),
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_docker_builds_challenge ON docker_builds(challenge_id);
CREATE INDEX idx_docker_builds_status ON docker_builds(status);
CREATE INDEX idx_docker_builds_queued ON docker_builds(queued_at);

-- ============================================================================
-- UPDATE CHALLENGES TABLE
-- ============================================================================

-- Add resource_type column to challenges for quick filtering
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS resource_type resource_type DEFAULT 'docker';

-- Add flag to indicate if challenge supports multiple resource types
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS supports_vm BOOLEAN DEFAULT FALSE;
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS supports_docker BOOLEAN DEFAULT TRUE;

CREATE INDEX idx_challenges_resource_type ON challenges(resource_type);

-- ============================================================================
-- UPDATE INSTANCES TABLE
-- ============================================================================

-- Add resource reference to instances
ALTER TABLE instances ADD COLUMN IF NOT EXISTS resource_id UUID REFERENCES challenge_resources(id);
ALTER TABLE instances ADD COLUMN IF NOT EXISTS resource_type resource_type DEFAULT 'docker';

CREATE INDEX idx_instances_resource ON instances(resource_id);
CREATE INDEX idx_instances_resource_type ON instances(resource_type);

-- ============================================================================
-- PLATFORM SETTINGS FOR VM SUPPORT
-- ============================================================================

INSERT INTO platform_settings (key, value, description) VALUES
    ('vm_enabled', 'true', 'Enable VM-based challenges'),
    ('vm_default_vcpu', '2', 'Default vCPUs for VMs'),
    ('vm_default_memory_mb', '2048', 'Default memory for VMs in MB'),
    ('vm_default_timeout', '14400', 'Default VM timeout in seconds (4 hours)'),
    ('vm_max_per_user', '2', 'Maximum concurrent VMs per user'),
    ('vm_vnc_port_range', '{"start": 5900, "end": 6100}', 'VNC port range'),
    ('vm_network_subnet', '"10.100.0.0/16"', 'VM network subnet'),
    ('upload_max_size_docker', '536870912', 'Max Docker context size in bytes (512MB)'),
    ('upload_max_size_vm', '53687091200', 'Max VM image size in bytes (50GB)'),
    ('upload_chunk_size', '10485760', 'Upload chunk size in bytes (10MB)'),
    ('upload_expiry_hours', '24', 'Hours before incomplete uploads expire')
ON CONFLICT (key) DO NOTHING;

-- ============================================================================
-- TRIGGERS
-- ============================================================================

CREATE TRIGGER update_uploads_updated_at BEFORE UPDATE ON uploads
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_vm_templates_updated_at BEFORE UPDATE ON vm_templates
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_challenge_resources_updated_at BEFORE UPDATE ON challenge_resources
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_vm_instances_updated_at BEFORE UPDATE ON vm_instances
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_docker_builds_updated_at BEFORE UPDATE ON docker_builds
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
