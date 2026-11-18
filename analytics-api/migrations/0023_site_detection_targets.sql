-- Fence key preparation by run without locking other detection tables.
ALTER TABLE console_sub_targets
    ADD COLUMN IF NOT EXISTS sync_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sync_key_state TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS probe_rpm INTEGER;
