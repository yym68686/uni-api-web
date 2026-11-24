ALTER TABLE console_configured_checks ADD COLUMN IF NOT EXISTS requested_scope TEXT NOT NULL DEFAULT '';
