-- The foreground only consumes a summary if no newer source writes remain
-- unprocessed. Writers append one row per group and transaction: no two
-- concurrent writers contend for the same row through nested triggers.
CREATE TABLE console_management_overview_dirty (
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL, writer XID8 NOT NULL,
 PRIMARY KEY(account_id,group_id,writer)
);
CREATE TABLE console_management_overview_reset (
 singleton BOOLEAN PRIMARY KEY CHECK(singleton), generation BIGINT NOT NULL
);
INSERT INTO console_management_overview_reset VALUES(true,0);
CREATE TABLE console_management_group_overview (
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL, generation BIGINT NOT NULL,
 catalog TEXT NOT NULL, body JSONB,
 PRIMARY KEY(account_id,group_id)
);

CREATE FUNCTION console_management_overview_dirty_v1() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE changes TEXT; columns_sql TEXT:=TG_ARGV[0];
BEGIN
 IF TG_OP='TRUNCATE' THEN
  UPDATE console_management_overview_reset SET generation=generation+1 WHERE singleton;
  RETURN NULL;
 ELSIF TG_OP='INSERT' THEN changes:='SELECT account_id,group_id FROM new_rows';
 ELSIF TG_OP='DELETE' THEN changes:='SELECT account_id,group_id FROM old_rows';
 ELSE changes:='(SELECT '||columns_sql||' FROM new_rows EXCEPT ALL SELECT '||columns_sql||' FROM old_rows) UNION ALL '||
  '(SELECT '||columns_sql||' FROM old_rows EXCEPT ALL SELECT '||columns_sql||' FROM new_rows)';
 END IF;
 EXECUTE 'INSERT INTO console_management_overview_dirty(account_id,group_id,writer)
  SELECT DISTINCT account_id,group_id,pg_current_xact_id() FROM ('||changes||') changed
  ON CONFLICT DO NOTHING';
 RETURN NULL;
END $$;

CREATE TRIGGER zz_overview_insert AFTER INSERT ON console_sub_models REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_update AFTER UPDATE ON console_sub_models REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1('account_id,group_id,model,state');
CREATE TRIGGER zz_overview_delete AFTER DELETE ON console_sub_models REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_truncate AFTER TRUNCATE ON console_sub_models FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
