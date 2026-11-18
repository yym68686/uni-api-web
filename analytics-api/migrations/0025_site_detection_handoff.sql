-- Keep suspended jobs invisible to pre-handoff workers during rolling upgrades.
ALTER TABLE console_sub_accounts
    ADD COLUMN IF NOT EXISTS sync_lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sync_resume_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sync_handoff_partial BOOLEAN NOT NULL DEFAULT false;
