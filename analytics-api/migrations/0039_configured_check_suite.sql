ALTER TABLE console_configured_checks ADD COLUMN IF NOT EXISTS full_model_check BOOLEAN NOT NULL DEFAULT FALSE;
