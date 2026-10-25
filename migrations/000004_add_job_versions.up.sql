-- Migration 000004: add job versions, progress and experiment fields
BEGIN;
ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1
        CHECK (version >= 1),
    ADD COLUMN IF NOT EXISTS stage text NOT NULL DEFAULT 'queued'
        CHECK (stage IN ('queued', 'starting', 'thumbnail_ready', 'preview_ready', 'display_ready')),
    ADD COLUMN IF NOT EXISTS variants_completed integer NOT NULL DEFAULT 0
        CHECK (variants_completed BETWEEN 0 AND 3),
    ADD COLUMN IF NOT EXISTS simulation_step integer NOT NULL DEFAULT 0
        CHECK (simulation_step >= 0);

-- Backfill progress for jobs that already left the queue, derived from the
-- variants that really exist in image_variants.
UPDATE jobs j
SET variants_completed = c.n,
    stage = CASE c.n
                WHEN 0 THEN 'starting'
                WHEN 1 THEN 'thumbnail_ready'
                WHEN 2 THEN 'preview_ready'
                ELSE 'display_ready'
            END
FROM (
    SELECT j2.id,
           LEAST(3, (SELECT count(*) FROM image_variants v WHERE v.image_id = j2.image_id))::int AS n
    FROM jobs j2
    WHERE j2.status <> 'queued'
) c
WHERE j.id = c.id;

END;
