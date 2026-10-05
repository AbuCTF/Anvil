ALTER TABLE challenges
    ADD COLUMN IF NOT EXISTS score_type TEXT NOT NULL DEFAULT 'static',
    ADD COLUMN IF NOT EXISTS score_minimum INTEGER,
    ADD COLUMN IF NOT EXISTS score_decay INTEGER NOT NULL DEFAULT 50;

UPDATE challenges
SET score_minimum = base_points
WHERE score_minimum IS NULL;

ALTER TABLE challenges
    ALTER COLUMN score_minimum SET DEFAULT 100,
    ALTER COLUMN score_minimum SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'challenges_score_type_check'
    ) THEN
        ALTER TABLE challenges
            ADD CONSTRAINT challenges_score_type_check CHECK (score_type IN ('static', 'dynamic'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'challenges_score_minimum_check'
    ) THEN
        ALTER TABLE challenges
            ADD CONSTRAINT challenges_score_minimum_check CHECK (score_minimum >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'challenges_score_range_check'
    ) THEN
        ALTER TABLE challenges
            ADD CONSTRAINT challenges_score_range_check CHECK (score_minimum <= base_points);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'challenges_score_decay_check'
    ) THEN
        ALTER TABLE challenges
            ADD CONSTRAINT challenges_score_decay_check CHECK (score_decay BETWEEN 1 AND 1000000);
    END IF;
END $$;
