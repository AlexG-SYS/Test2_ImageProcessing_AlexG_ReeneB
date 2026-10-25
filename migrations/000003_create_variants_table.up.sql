BEGIN;

CREATE TABLE IF NOT EXISTS image_variants (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    image_id        uuid NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (name IN ('thumbnail', 'preview', 'display')),
    stored_filename text NOT NULL,
    width           integer NOT NULL,
    height          integer NOT NULL,
    size_bytes      bigint NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (image_id, name)
);

END;