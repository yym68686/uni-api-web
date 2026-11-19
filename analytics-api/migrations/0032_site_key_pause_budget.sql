-- Keep the existing counter for model pauses; give key preparation its own.
ALTER TABLE console_sub_accounts
    ADD COLUMN IF NOT EXISTS sync_key_pause_count INTEGER NOT NULL DEFAULT 0;
