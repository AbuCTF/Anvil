-- Retain the capacity reservation chosen for a generic VM instance so every
-- stop path can release the same node and resource amounts it acquired.
ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS vm_node_id UUID REFERENCES vm_nodes(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS reserved_vcpu INTEGER,
    ADD COLUMN IF NOT EXISTS reserved_memory_mb INTEGER;

ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_reserved_vcpu_nonnegative,
    DROP CONSTRAINT IF EXISTS instances_reserved_memory_nonnegative;

ALTER TABLE instances
    ADD CONSTRAINT instances_reserved_vcpu_nonnegative
        CHECK (reserved_vcpu IS NULL OR reserved_vcpu >= 0),
    ADD CONSTRAINT instances_reserved_memory_nonnegative
        CHECK (reserved_memory_mb IS NULL OR reserved_memory_mb >= 0);

CREATE INDEX IF NOT EXISTS idx_instances_vm_node ON instances(vm_node_id);
