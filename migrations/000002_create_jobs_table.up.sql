BEGIN;

CREATE TABLE IF NOT EXISTS jobs (

    id        uuid        PRIMARY KEY DEFAULT uuidv7(),
    public_id UUID    NOT NULL DEFAULT uuidv4(),
    image_id      uuid NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    status        text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'processing', 'completed', 'failed')),
    error_message text,
    queued_at     timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    completed_at  timestamptz
);

-- Supports ClaimNext's "next queued job" query, which orders by queued_at ascending and then id ascending to break ties.
CREATE INDEX IF NOT EXISTS idx_jobs_status_queued_at ON jobs (status, queued_at);

END;