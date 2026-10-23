-- Live account balances are read on every page validation and overlaid onto
-- the current channel computation. Login session rotation is not a channel
-- result input. Keep a separate header epoch so older code still invalidates
-- its cached whole response during a rolling release.
INSERT INTO console_management_view_epoch(relation_name) VALUES('console_sub_account_headers');
CREATE OR REPLACE FUNCTION console_management_view_epoch_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN:=true;columns_sql TEXT:=TG_ARGV[0];
BEGIN
 IF TG_OP='UPDATE' THEN
  EXECUTE 'SELECT EXISTS((SELECT '||columns_sql||' FROM new_rows EXCEPT ALL SELECT '||columns_sql||' FROM old_rows) UNION ALL (SELECT '||columns_sql||' FROM old_rows EXCEPT ALL SELECT '||columns_sql||' FROM new_rows))' INTO changed;
 ELSIF TG_OP='INSERT' THEN SELECT EXISTS(SELECT 1 FROM new_rows) INTO changed;
 ELSIF TG_OP='DELETE' THEN SELECT EXISTS(SELECT 1 FROM old_rows) INTO changed;
 END IF;
 IF changed THEN EXECUTE format('UPDATE %I.console_management_view_epoch SET revision=revision+1,last_xid=pg_current_xact_id() WHERE relation_name=$1 AND last_xid IS DISTINCT FROM pg_current_xact_id()',TG_TABLE_SCHEMA) USING COALESCE(NULLIF(TG_ARGV[1],''),TG_TABLE_NAME);END IF;
 RETURN NULL;
END $$;
DROP TRIGGER console_sub_accounts_view_update ON console_sub_accounts;
CREATE TRIGGER console_sub_accounts_view_update AFTER UPDATE ON console_sub_accounts REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_login_password,state,message,job_kind,synced_at,created_at,provider_kind,login_name');
CREATE TRIGGER console_sub_accounts_headers_update AFTER UPDATE ON console_sub_accounts REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,balance,encrypted_auth','console_sub_account_headers');
