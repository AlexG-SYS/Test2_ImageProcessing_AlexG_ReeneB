-- [V2] Roll back migration 000004.
BEGIN;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS simulation_step,
    DROP COLUMN IF EXISTS variants_completed,
    DROP COLUMN IF EXISTS stage,
    DROP COLUMN IF EXISTS version;
END;
