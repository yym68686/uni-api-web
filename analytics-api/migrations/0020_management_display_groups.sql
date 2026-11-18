-- Derived list cache only. No startup scan or backfill.
CREATE TABLE console_management_display_groups (
 owner_key TEXT NOT NULL,
 account_id TEXT NOT NULL,
 group_id BIGINT NOT NULL,
 version TEXT NOT NULL,
 local_version TEXT NOT NULL DEFAULT '',
 observed_at TIMESTAMPTZ NOT NULL,
 raw_bytes INTEGER NOT NULL CHECK (raw_bytes BETWEEN 2 AND 2097152),
 body BYTEA NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 524288),
 PRIMARY KEY(owner_key,account_id,group_id)
);

-- Record the last writer, in the same transaction as the source change.
-- Dependencies are per group. The reader uses that narrower proof only
-- after verifying EVERY probe resolves against the same account and group.
CREATE TABLE console_management_display_epochs (
 kind TEXT NOT NULL, account_id TEXT NOT NULL, group_id BIGINT NOT NULL,
 writer XID8 NOT NULL, PRIMARY KEY(kind,account_id,group_id)
);
CREATE FUNCTION console_management_display_epoch_v1() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE scope TEXT:=TG_ARGV[0]; key_sql TEXT; write_xid XID8;
BEGIN
 IF TG_OP='TRUNCATE' THEN
  INSERT INTO console_management_display_epochs VALUES('reset','',0,pg_current_xact_id())
   ON CONFLICT(kind,account_id,group_id) DO UPDATE SET writer=EXCLUDED.writer;
  RETURN NULL;
 END IF;
 key_sql:=CASE WHEN TG_TABLE_NAME='console_management_receipt_signal'
  THEN 'id,account_id' ELSE 'account_id,group_id' END;
 IF TG_OP='INSERT' THEN
  key_sql:='SELECT '||key_sql||' FROM new_rows';
 ELSIF TG_OP='DELETE' THEN
  key_sql:='SELECT '||key_sql||' FROM old_rows';
 ELSE
  key_sql:='SELECT '||key_sql||' FROM ((SELECT '||TG_ARGV[1]||' FROM new_rows EXCEPT ALL SELECT '||TG_ARGV[1]||' FROM old_rows)'||
  ' UNION ALL (SELECT '||TG_ARGV[1]||' FROM old_rows EXCEPT ALL SELECT '||TG_ARGV[1]||' FROM new_rows)) changed';
 END IF;
 IF TG_TABLE_NAME='console_management_receipt_signal' THEN
  -- Deleted or moved source IDs may be absent here. The usage trigger
  -- independently advances the OLD and NEW source groups in that case.
  key_sql:='SELECT u.account_id,u.group_id FROM ('||key_sql||') r
  JOIN console_sub_usage u ON u.id=r.id AND u.account_id=r.account_id';
 END IF;
 write_xid:=pg_current_xact_id();
 EXECUTE 'INSERT INTO console_management_display_epochs(kind,account_id,group_id,writer)
 SELECT $1,d.account_id,d.group_id,$2 FROM (SELECT DISTINCT account_id,group_id FROM ('||key_sql||') dependencies) d
 ORDER BY d.account_id,d.group_id
 ON CONFLICT(kind,account_id,group_id) DO UPDATE SET writer=EXCLUDED.writer
 WHERE console_management_display_epochs.writer<>EXCLUDED.writer' USING scope,write_xid;
 RETURN NULL;
END $$;

DO $$
DECLARE item RECORD;
BEGIN
 FOR item IN SELECT * FROM (VALUES
  ('console_sub_models','model','account_id,group_id,model,state,message'),
  ('console_sub_model_summaries','model','*'),
  ('console_management_model_signal','model','*'),
  ('console_sub_usage','receipt','id,account_id,group_id'),
  ('console_management_receipt_signal','receipt','*')
 ) AS sources(relation_name,kind,columns_sql)
 LOOP
  EXECUTE format('CREATE TRIGGER zz_display_epoch_insert AFTER INSERT ON %I REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_display_epoch_v1(%L,%L)',item.relation_name,item.kind,item.columns_sql);
  EXECUTE format('CREATE TRIGGER zz_display_epoch_update AFTER UPDATE ON %I REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_display_epoch_v1(%L,%L)',item.relation_name,item.kind,item.columns_sql);
  EXECUTE format('CREATE TRIGGER zz_display_epoch_delete AFTER DELETE ON %I REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_display_epoch_v1(%L,%L)',item.relation_name,item.kind,item.columns_sql);
  EXECUTE format('CREATE TRIGGER zz_display_epoch_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION console_management_display_epoch_v1(%L,%L)',item.relation_name,item.kind,item.columns_sql);
 END LOOP;
END $$;
