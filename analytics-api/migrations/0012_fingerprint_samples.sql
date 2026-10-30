-- Private, bounded numeric evidence. Public result JSON never includes it.
-- Nullable, no default/backfill: adding the column does not rewrite old rows.
ALTER TABLE console_model_fingerprints ADD COLUMN IF NOT EXISTS sample_state JSONB;
