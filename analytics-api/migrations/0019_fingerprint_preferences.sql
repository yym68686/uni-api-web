ALTER TABLE console_configured_checks ADD COLUMN IF NOT EXISTS owner TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS console_fingerprint_preferences (
    owner TEXT PRIMARY KEY,
    models JSONB NOT NULL CHECK (jsonb_typeof(models) = 'array')
);

CREATE TABLE IF NOT EXISTS console_fingerprint_upstream_catalog (
    id BOOLEAN PRIMARY KEY CHECK (id),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    bundle BYTEA NOT NULL CHECK (octet_length(bundle) <= 16777216)
);
