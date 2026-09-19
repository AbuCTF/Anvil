-- 026_economy_point_events_nullable.sql
-- Point events that aren't tied to a specific challenge (convert_out at
-- points->credits time, freeze_convert_in at freeze) need a null challenge_id.
ALTER TABLE economy_point_events ALTER COLUMN challenge_id DROP NOT NULL;
