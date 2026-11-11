-- Console-only conversion. Upstream billing evidence and gateway routes stay raw.
ALTER TABLE console_sub_accounts ADD COLUMN rate_coefficient NUMERIC NOT NULL DEFAULT 1
 CHECK(rate_coefficient>0 AND rate_coefficient<=1000000);
DROP TRIGGER console_sub_accounts_view_update ON console_sub_accounts;
CREATE TRIGGER console_sub_accounts_view_update AFTER UPDATE ON console_sub_accounts REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_login_password,state,message,job_kind,synced_at,created_at,provider_kind,login_name,rate_coefficient');
