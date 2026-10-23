-- No startup scan. Missing rows use the same current summary function; an
-- explicit bounded preparation command fills legacy targets after deployment.
CREATE TABLE console_sub_capability_summaries (
 account_id TEXT NOT NULL,group_id BIGINT NOT NULL,
 compaction JSONB,tool_use JSONB,
 PRIMARY KEY(account_id,group_id),
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE FUNCTION console_sub_capability_summary_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO console_sub_capability_summaries(account_id,group_id,compaction,tool_use)
 VALUES(NEW.account_id,NEW.group_id,console_sub_capability_summary_v1(NEW.compaction),console_sub_capability_summary_v1(NEW.tool_use))
 ON CONFLICT(account_id,group_id) DO UPDATE SET compaction=EXCLUDED.compaction,tool_use=EXCLUDED.tool_use;
 RETURN NEW;
END $$;
CREATE TRIGGER console_sub_capability_summary_write_v1 AFTER INSERT OR UPDATE OF account_id,group_id,compaction,tool_use ON console_sub_targets
 FOR EACH ROW EXECUTE FUNCTION console_sub_capability_summary_write_v1();
