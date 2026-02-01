-- Migration 004: Audit logging and platform settings
-- Adds comprehensive audit logging and admin-configurable settings

-- Audit log for tracking all user actions
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL,
    details JSONB DEFAULT '{}',
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Index for querying audit logs
CREATE INDEX IF NOT EXISTS idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at DESC);

-- Platform settings table for admin-configurable options
CREATE TABLE IF NOT EXISTS platform_settings (
    key VARCHAR(100) PRIMARY KEY,
    value JSONB NOT NULL,
    description TEXT,
    category VARCHAR(50) DEFAULT 'general',
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by UUID REFERENCES users(id)
);

-- Default platform settings
INSERT INTO platform_settings (key, value, description, category) VALUES
    -- Instance settings
    ('instance.default_timeout_minutes', '60', 'Default instance timeout in minutes', 'instances'),
    ('instance.max_extensions', '3', 'Maximum number of extensions per instance', 'instances'),
    ('instance.extension_minutes', '30', 'Minutes added per extension', 'instances'),
    ('instance.max_per_user', '2', 'Maximum concurrent instances per user', 'instances'),
    
    -- Cooldown settings by difficulty
    ('cooldown.easy_minutes', '5', 'Cooldown for easy challenges (minutes)', 'cooldowns'),
    ('cooldown.medium_minutes', '10', 'Cooldown for medium challenges (minutes)', 'cooldowns'),
    ('cooldown.hard_minutes', '15', 'Cooldown for hard challenges (minutes)', 'cooldowns'),
    ('cooldown.insane_minutes', '20', 'Cooldown for insane challenges (minutes)', 'cooldowns'),
    
    -- VM settings
    ('vm.default_vcpu', '2', 'Default vCPUs for VM instances', 'vms'),
    ('vm.default_memory_mb', '2048', 'Default memory for VM instances (MB)', 'vms'),
    ('vm.max_per_user', '1', 'Maximum concurrent VMs per user', 'vms'),
    
    -- VPN settings
    ('vpn.enabled', 'true', 'Whether VPN is enabled', 'vpn'),
    ('vpn.max_configs_per_user', '1', 'Max VPN configs per user', 'vpn'),
    
    -- Platform general
    ('platform.registration_enabled', 'true', 'Allow new user registrations', 'general'),
    ('platform.maintenance_mode', 'false', 'Platform maintenance mode', 'general'),
    ('platform.require_vpn', 'true', 'Require VPN connection to start instances', 'general')
ON CONFLICT (key) DO NOTHING;

-- Function to get setting value
CREATE OR REPLACE FUNCTION get_setting(setting_key VARCHAR, default_value JSONB DEFAULT NULL)
RETURNS JSONB AS $$
DECLARE
    result JSONB;
BEGIN
    SELECT value INTO result FROM platform_settings WHERE key = setting_key;
    RETURN COALESCE(result, default_value);
END;
$$ LANGUAGE plpgsql;

-- Function to get setting as integer
CREATE OR REPLACE FUNCTION get_setting_int(setting_key VARCHAR, default_value INT DEFAULT 0)
RETURNS INT AS $$
DECLARE
    result JSONB;
BEGIN
    SELECT value INTO result FROM platform_settings WHERE key = setting_key;
    IF result IS NULL THEN
        RETURN default_value;
    END IF;
    RETURN (result::TEXT)::INT;
END;
$$ LANGUAGE plpgsql;

-- Add stopped_at column to instances if not exists
ALTER TABLE instances ADD COLUMN IF NOT EXISTS stopped_at TIMESTAMPTZ;

-- Add error_message column to instances if not exists  
ALTER TABLE instances ADD COLUMN IF NOT EXISTS error_message TEXT;

-- Add vm_id column to instances for VM-based challenges
ALTER TABLE instances ADD COLUMN IF NOT EXISTS vm_id VARCHAR(100);

-- Update vpn_configs to track last connection
ALTER TABLE vpn_configs ADD COLUMN IF NOT EXISTS last_handshake TIMESTAMPTZ;
ALTER TABLE vpn_configs ADD COLUMN IF NOT EXISTS bytes_sent BIGINT DEFAULT 0;
ALTER TABLE vpn_configs ADD COLUMN IF NOT EXISTS bytes_received BIGINT DEFAULT 0;

-- Index for active VPN connections
CREATE INDEX IF NOT EXISTS idx_vpn_configs_last_handshake ON vpn_configs(last_handshake DESC) WHERE last_handshake IS NOT NULL;

-- View for instance statistics
CREATE OR REPLACE VIEW instance_stats AS
SELECT 
    DATE_TRUNC('hour', created_at) as hour,
    COUNT(*) as total_started,
    COUNT(*) FILTER (WHERE status = 'running') as currently_running,
    COUNT(*) FILTER (WHERE status = 'stopped') as stopped,
    COUNT(*) FILTER (WHERE status = 'failed') as failed,
    AVG(EXTRACT(EPOCH FROM (COALESCE(stopped_at, NOW()) - created_at))) as avg_duration_seconds
FROM instances
WHERE created_at > NOW() - INTERVAL '7 days'
GROUP BY DATE_TRUNC('hour', created_at)
ORDER BY hour DESC;

-- View for user activity
CREATE OR REPLACE VIEW user_activity AS
SELECT 
    u.id as user_id,
    u.username,
    COUNT(DISTINCT i.id) as total_instances,
    COUNT(DISTINCT i.id) FILTER (WHERE i.status = 'running') as active_instances,
    COUNT(DISTINCT s.id) as total_submissions,
    COUNT(DISTINCT s.id) FILTER (WHERE s.is_correct = true) as correct_submissions,
    MAX(i.created_at) as last_instance,
    MAX(s.created_at) as last_submission
FROM users u
LEFT JOIN instances i ON u.id = i.user_id
LEFT JOIN submissions s ON u.id = s.user_id
GROUP BY u.id, u.username;

COMMENT ON TABLE audit_log IS 'Comprehensive audit trail for all user actions';
COMMENT ON TABLE platform_settings IS 'Admin-configurable platform settings';
