-- 011_koth_hill_host.sql
-- KotH hills are shared services at a fixed address.

ALTER TABLE game_koth_hills ADD COLUMN host INET;
