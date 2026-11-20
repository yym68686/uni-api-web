ALTER TABLE console_sub_targets
    ADD COLUMN IF NOT EXISTS newapi_missing_since TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS newapi_last_missing_at TIMESTAMPTZ;

ALTER TABLE console_site_key_creates
    ADD COLUMN IF NOT EXISTS acknowledged_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS acknowledged_base TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS owned_key_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS owned_user_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS owned_created_at BIGINT NOT NULL DEFAULT 0;

-- No credential or upstream response body is retained. Keep the evidence
-- even if the site account is deleted, to avoid losing the audit trail.
CREATE TABLE IF NOT EXISTS console_newapi_key_reclaims (
    account_id TEXT NOT NULL,
    base TEXT NOT NULL,
    attempt_scope TEXT NOT NULL,
    group_id BIGINT NOT NULL,
    remote_key_id BIGINT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'deleted', 'rejected', 'not_sent')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (account_id, attempt_scope)
);
CREATE UNIQUE INDEX IF NOT EXISTS console_newapi_key_reclaims_once
    ON console_newapi_key_reclaims(account_id, base, remote_key_id) WHERE state <> 'not_sent';
CREATE UNIQUE INDEX IF NOT EXISTS console_newapi_key_reclaims_unresolved
    ON console_newapi_key_reclaims(account_id, base) WHERE state = 'pending';
