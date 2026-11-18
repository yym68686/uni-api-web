CREATE TABLE IF NOT EXISTS console_fingerprint_catalog (
    id BOOLEAN PRIMARY KEY CHECK (id),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    bundle BYTEA NOT NULL CHECK (octet_length(bundle) <= 16777216)
);
