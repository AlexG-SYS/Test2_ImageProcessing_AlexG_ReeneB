BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS images (
    id        uuid    PRIMARY KEY DEFAULT uuidv7(),
    public_id UUID    NOT NULL DEFAULT uuidv4(),
    original_filename text NOT NULL,
    stored_filename   text NOT NULL,
    media_type        text NOT NULL,
    size_bytes        bigint NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

END;