CREATE TABLE IF NOT EXISTS instruments (
    id          TEXT PRIMARY KEY,           -- manifest ID; matches [Header] in .bt exactly
    name        TEXT NOT NULL,              -- human-readable label
    sample_path TEXT NOT NULL,              -- absolute, resolved path to the .wav sample
    config      TEXT NOT NULL DEFAULT '{}', -- verbatim JSON config blob
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
