-- 022_challenge_teaser.sql
-- Pre-launch sub_description (Abu 2026-09-19): with launch-gating (economy), a
-- challenge's card shows name + category + difficulty + points + this short
-- plain-text blurb, while the full description + files + instance stay gated behind
-- launch. Optional; NULL/empty => the card shows metadata only. Additive.

ALTER TABLE challenges ADD COLUMN IF NOT EXISTS sub_description VARCHAR(255);
