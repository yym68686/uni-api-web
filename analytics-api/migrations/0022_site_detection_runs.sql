-- Persist each active detection plan and its timing across workers and releases.
ALTER TABLE console_sub_accounts
    ADD COLUMN IF NOT EXISTS sync_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sync_phase TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sync_plan_ready BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS sync_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sync_pause_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS probe_limits JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS console_site_detection_limits (
    scope TEXT PRIMARY KEY,
    retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    learned_rpm INTEGER NOT NULL DEFAULT 0,
    success_streak INTEGER NOT NULL DEFAULT 0,
    learned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS console_site_detection_slots (
    scope TEXT NOT NULL,
    slot TEXT NOT NULL,
    until_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(scope,slot)
);
