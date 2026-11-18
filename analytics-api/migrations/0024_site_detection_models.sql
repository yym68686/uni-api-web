-- Fence model progress by run without locking other detection tables.
ALTER TABLE console_sub_models
    ADD COLUMN IF NOT EXISTS sync_run_id TEXT NOT NULL DEFAULT '';
