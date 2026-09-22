-- Optional multi-container (compose-style) challenges. NULL = single-image
-- challenge (uses container_image/exposed_ports as before), so every existing
-- row and code path is unchanged.
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS container_spec JSONB;
COMMENT ON COLUMN challenges.container_spec IS
  'Optional multi-container service list (compose-style); NULL = single-image challenge (container_image/exposed_ports).';
