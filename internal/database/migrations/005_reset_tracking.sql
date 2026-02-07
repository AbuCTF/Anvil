-- Migration 005: Add reset tracking to prevent abuse
-- Adds reset_count and max_resets fields to instances table

-- Add reset tracking columns to instances
ALTER TABLE instances ADD COLUMN IF NOT EXISTS reset_count INTEGER DEFAULT 0;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS max_resets INTEGER DEFAULT 3;

-- Add index for querying reset limits
CREATE INDEX IF NOT EXISTS idx_instances_reset_count ON instances(reset_count);

-- Add max_resets to challenges table for per-challenge limits
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS max_resets INTEGER DEFAULT 3;

-- Update existing instances to have default max_resets
UPDATE instances SET max_resets = 3 WHERE max_resets IS NULL;
UPDATE challenges SET max_resets = 3 WHERE max_resets IS NULL;
