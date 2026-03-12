-- Add container_platform column for cross-arch support (e.g., linux/amd64 on arm64 hosts)
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS container_platform TEXT DEFAULT '';
