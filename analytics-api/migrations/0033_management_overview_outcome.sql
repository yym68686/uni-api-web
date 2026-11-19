-- Only the three legacy interruption messages affect the overview category.
-- Compare the classified state, not arbitrary changes to diagnostic text.
CREATE FUNCTION console_management_overview_model_state_v1(model_state TEXT, message TEXT) RETURNS TEXT
LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN model_state='interrupted' AND message IN (
  '上游拒绝测试凭据或访问权限', '上游余额或配额不足', '上游拒绝或限制本次请求，保留上次检测结论'
 ) THEN 'rejected' ELSE model_state END
$$;

DROP TRIGGER zz_overview_update ON console_sub_models;
CREATE TRIGGER zz_overview_update AFTER UPDATE ON console_sub_models
 REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
 FOR EACH STATEMENT EXECUTE FUNCTION console_management_overview_dirty_v1(
  'account_id,group_id,model,console_management_overview_model_state_v1(state,message) AS state'
 );

-- Previous versions may have missed a classification-only change.
UPDATE console_management_overview_reset SET generation=generation+1 WHERE singleton;
