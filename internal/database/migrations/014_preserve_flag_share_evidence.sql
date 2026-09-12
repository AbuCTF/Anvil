-- Preserve dynamic-flag sharing evidence when its short-lived instance is removed.
ALTER TABLE flag_share_events
    DROP CONSTRAINT IF EXISTS flag_share_events_owner_instance_id_fkey;

ALTER TABLE flag_share_events
    ADD CONSTRAINT flag_share_events_owner_instance_id_fkey
    FOREIGN KEY (owner_instance_id) REFERENCES instances(id) ON DELETE SET NULL;

COMMENT ON COLUMN flag_share_events.owner_instance_id IS
    'Originating instance when still available; NULL for regex matches or cleaned-up instances';
