CREATE TABLE IF NOT EXISTS console_managed_config_versions (
    source_id text NOT NULL REFERENCES console_sources(id),
    revision bigint GENERATED ALWAYS AS IDENTITY,
    encrypted_bundle text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, revision)
);

CREATE TABLE IF NOT EXISTS console_managed_config_operations (
    source_id text NOT NULL REFERENCES console_sources(id),
    operation_id text NOT NULL,
    operation_hash text NOT NULL,
    revision bigint NOT NULL,
    credits double precision NOT NULL,
    PRIMARY KEY (source_id, operation_id)
);

CREATE TABLE IF NOT EXISTS console_managed_config_state (
    source_id text PRIMARY KEY REFERENCES console_sources(id),
    published_revision bigint,
    draft_revision bigint,
    requested boolean NOT NULL DEFAULT false,
    failure text NOT NULL DEFAULT '',
    FOREIGN KEY (source_id, published_revision) REFERENCES console_managed_config_versions(source_id, revision),
    FOREIGN KEY (source_id, draft_revision) REFERENCES console_managed_config_versions(source_id, revision)
);
