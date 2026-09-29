ALTER TABLE challenges ADD COLUMN IF NOT EXISTS delivery_type VARCHAR(16);

UPDATE challenges c
SET delivery_type = CASE
    WHEN c.resource_type = 'vm' THEN 'vm'
    WHEN COALESCE(c.container_image, '') <> '' OR c.container_spec IS NOT NULL THEN 'docker'
    WHEN EXISTS (SELECT 1 FROM challenge_attachments ca WHERE ca.challenge_id = c.id) THEN 'static'
    ELSE 'external'
END
WHERE c.delivery_type IS NULL;

ALTER TABLE challenges ALTER COLUMN delivery_type SET DEFAULT 'docker';
ALTER TABLE challenges ALTER COLUMN delivery_type SET NOT NULL;

DO $$
BEGIN
    ALTER TABLE challenges ADD CONSTRAINT challenges_delivery_type_check
        CHECK (delivery_type IN ('docker', 'static', 'external', 'vm'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;
