-- Read the queue live on every covered page and rebuild the group after it
-- drains, independently of whether the model writer also advanced.
CREATE TRIGGER zz_overview_queue_insert AFTER INSERT ON console_sub_check_queue REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_queue_update AFTER UPDATE ON console_sub_check_queue REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1('account_id,group_id,kind,model');
CREATE TRIGGER zz_overview_queue_delete AFTER DELETE ON console_sub_check_queue REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
CREATE TRIGGER zz_overview_queue_truncate AFTER TRUNCATE ON console_sub_check_queue FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1();
