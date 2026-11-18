-- Recreating a target must not inherit the previous incarnation's summary.
-- Legacy Astra results are already patched in from the live target document.
CREATE TRIGGER zz_overview_target_insert AFTER INSERT ON console_sub_targets REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_target_update AFTER UPDATE ON console_sub_targets REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1('account_id,group_id');
CREATE TRIGGER zz_overview_target_delete AFTER DELETE ON console_sub_targets REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_target_truncate AFTER TRUNCATE ON console_sub_targets FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
