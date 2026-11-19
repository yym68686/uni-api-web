-- Only New API key-create scopes use this counter. Leave model RPM learning alone.
ALTER TABLE console_site_detection_limits
    ADD COLUMN IF NOT EXISTS create_rate_failures INTEGER NOT NULL DEFAULT 0;
