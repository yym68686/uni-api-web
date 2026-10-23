-- Exact invalidation for reusable management computations. Readers must still
-- validate this epoch, semantic evidence counters, prices, deadlines and both
-- live gateway credentials before publishing a previous computation.
CREATE TABLE console_management_view_epoch(relation_name TEXT PRIMARY KEY,revision BIGINT NOT NULL DEFAULT 0,last_xid XID8);
INSERT INTO console_management_view_epoch(relation_name) VALUES
('console_sources'),
('console_control_snapshots'),
('console_sub_accounts'),
('console_sub_targets'),
('console_sub_model_summaries'),
('console_sub_key_index'),
('console_sub_observed_rates'),
('console_sub_check_queue'),
('console_quality_history'),
('console_quality_group_links'),
('console_configured_checks'),
('console_model_fingerprints'),
('console_sub_capability_summaries'),
('console_sub_usage');
CREATE FUNCTION console_management_view_epoch_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN:=true;columns_sql TEXT:=TG_ARGV[0];
BEGIN
 IF TG_OP='UPDATE' THEN
  EXECUTE 'SELECT EXISTS((SELECT '||columns_sql||' FROM new_rows EXCEPT ALL SELECT '||columns_sql||' FROM old_rows) UNION ALL (SELECT '||columns_sql||' FROM old_rows EXCEPT ALL SELECT '||columns_sql||' FROM new_rows))' INTO changed;
 ELSIF TG_OP='INSERT' THEN SELECT EXISTS(SELECT 1 FROM new_rows) INTO changed;
 ELSIF TG_OP='DELETE' THEN SELECT EXISTS(SELECT 1 FROM old_rows) INTO changed;
 END IF;
 IF changed THEN EXECUTE format('UPDATE %I.console_management_view_epoch SET revision=revision+1,last_xid=pg_current_xact_id() WHERE relation_name=$1 AND last_xid IS DISTINCT FROM pg_current_xact_id()',TG_TABLE_SCHEMA) USING TG_TABLE_NAME;END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER console_sources_view_insert AFTER INSERT ON console_sources REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sources_view_update AFTER UPDATE ON console_sources REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sources_view_delete AFTER DELETE ON console_sources REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sources_view_truncate AFTER TRUNCATE ON console_sources  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_control_snapshots_view_insert AFTER INSERT ON console_control_snapshots REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('source_id,encrypted_snapshot,target_hash,revision');
CREATE TRIGGER console_control_snapshots_view_update AFTER UPDATE ON console_control_snapshots REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('source_id,encrypted_snapshot,target_hash,revision');
CREATE TRIGGER console_control_snapshots_view_delete AFTER DELETE ON console_control_snapshots REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('source_id,encrypted_snapshot,target_hash,revision');
CREATE TRIGGER console_control_snapshots_view_truncate AFTER TRUNCATE ON console_control_snapshots  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('source_id,encrypted_snapshot,target_hash,revision');
CREATE TRIGGER console_sub_accounts_view_insert AFTER INSERT ON console_sub_accounts REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_auth,encrypted_login_password,state,message,job_kind,synced_at,created_at,balance,provider_kind,login_name');
CREATE TRIGGER console_sub_accounts_view_update AFTER UPDATE ON console_sub_accounts REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_auth,encrypted_login_password,state,message,job_kind,synced_at,created_at,balance,provider_kind,login_name');
CREATE TRIGGER console_sub_accounts_view_delete AFTER DELETE ON console_sub_accounts REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_auth,encrypted_login_password,state,message,job_kind,synced_at,created_at,balance,provider_kind,login_name');
CREATE TRIGGER console_sub_accounts_view_truncate AFTER TRUNCATE ON console_sub_accounts  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,owner,name,base,email,encrypted_auth,encrypted_login_password,state,message,job_kind,synced_at,created_at,balance,provider_kind,login_name');
CREATE TRIGGER console_sub_targets_view_insert AFTER INSERT ON console_sub_targets REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_targets_view_update AFTER UPDATE ON console_sub_targets REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_targets_view_delete AFTER DELETE ON console_sub_targets REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_targets_view_truncate AFTER TRUNCATE ON console_sub_targets  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_model_summaries_view_insert AFTER INSERT ON console_sub_model_summaries REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_model_summaries_view_update AFTER UPDATE ON console_sub_model_summaries REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_model_summaries_view_delete AFTER DELETE ON console_sub_model_summaries REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_model_summaries_view_truncate AFTER TRUNCATE ON console_sub_model_summaries  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_key_index_view_insert AFTER INSERT ON console_sub_key_index REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_key_index_view_update AFTER UPDATE ON console_sub_key_index REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_key_index_view_delete AFTER DELETE ON console_sub_key_index REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_key_index_view_truncate AFTER TRUNCATE ON console_sub_key_index  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_observed_rates_view_insert AFTER INSERT ON console_sub_observed_rates REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_observed_rates_view_update AFTER UPDATE ON console_sub_observed_rates REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_observed_rates_view_delete AFTER DELETE ON console_sub_observed_rates REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_observed_rates_view_truncate AFTER TRUNCATE ON console_sub_observed_rates  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_check_queue_view_insert AFTER INSERT ON console_sub_check_queue REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_check_queue_view_update AFTER UPDATE ON console_sub_check_queue REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_check_queue_view_delete AFTER DELETE ON console_sub_check_queue REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_check_queue_view_truncate AFTER TRUNCATE ON console_sub_check_queue  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_history_view_insert AFTER INSERT ON console_quality_history REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_history_view_update AFTER UPDATE ON console_quality_history REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_history_view_delete AFTER DELETE ON console_quality_history REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_history_view_truncate AFTER TRUNCATE ON console_quality_history  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_group_links_view_insert AFTER INSERT ON console_quality_group_links REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_group_links_view_update AFTER UPDATE ON console_quality_group_links REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_group_links_view_delete AFTER DELETE ON console_quality_group_links REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_quality_group_links_view_truncate AFTER TRUNCATE ON console_quality_group_links  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_configured_checks_view_insert AFTER INSERT ON console_configured_checks REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_configured_checks_view_update AFTER UPDATE ON console_configured_checks REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_configured_checks_view_delete AFTER DELETE ON console_configured_checks REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_configured_checks_view_truncate AFTER TRUNCATE ON console_configured_checks  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_model_fingerprints_view_insert AFTER INSERT ON console_model_fingerprints REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_model_fingerprints_view_update AFTER UPDATE ON console_model_fingerprints REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_model_fingerprints_view_delete AFTER DELETE ON console_model_fingerprints REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_model_fingerprints_view_truncate AFTER TRUNCATE ON console_model_fingerprints  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_capability_summaries_view_insert AFTER INSERT ON console_sub_capability_summaries REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_capability_summaries_view_update AFTER UPDATE ON console_sub_capability_summaries REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_capability_summaries_view_delete AFTER DELETE ON console_sub_capability_summaries REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_capability_summaries_view_truncate AFTER TRUNCATE ON console_sub_capability_summaries  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('*');
CREATE TRIGGER console_sub_usage_view_insert AFTER INSERT ON console_sub_usage REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,account_id,group_id,key_id,model,started_at,status');
CREATE TRIGGER console_sub_usage_view_update AFTER UPDATE ON console_sub_usage REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,account_id,group_id,key_id,model,started_at,status');
CREATE TRIGGER console_sub_usage_view_delete AFTER DELETE ON console_sub_usage REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,account_id,group_id,key_id,model,started_at,status');
CREATE TRIGGER console_sub_usage_view_truncate AFTER TRUNCATE ON console_sub_usage  FOR EACH STATEMENT EXECUTE FUNCTION console_management_view_epoch_write_v1('id,account_id,group_id,key_id,model,started_at,status');
