CREATE TABLE IF NOT EXISTS console_channel_model_catalogs (
    source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_identity TEXT NOT NULL,
    models JSONB NOT NULL DEFAULT '{}',
    refreshed_at TIMESTAMPTZ,
    PRIMARY KEY (source_id, provider, upstream_identity)
);
