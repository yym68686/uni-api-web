-- Keep the last completed verdict separate from the active round and samples.
ALTER TABLE console_model_fingerprints ADD COLUMN IF NOT EXISTS previous_verdict JSONB;
