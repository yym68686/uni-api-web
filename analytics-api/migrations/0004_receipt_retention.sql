-- Existing production tables prepare these indexes CONCURRENTLY with the
-- explicit migration tool before activation. On a fresh database they are empty.
CREATE INDEX IF NOT EXISTS console_sub_spend_retention ON console_sub_spend_logs(at_ms);
CREATE INDEX IF NOT EXISTS console_sub_usage_retention ON console_sub_usage(account_id,started_at) WHERE status<>'pending';
