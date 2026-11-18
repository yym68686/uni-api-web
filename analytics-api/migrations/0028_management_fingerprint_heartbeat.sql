-- Retain all row changes except extending a still-live lease. Readers still
-- compare deadline fences and the worker still enforces the live lease.
CREATE FUNCTION console_management_fingerprint_epoch_update_v1() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM old_rows o FULL JOIN new_rows n USING(scope,model)
  WHERE o.scope IS NULL OR n.scope IS NULL OR
    (o IS DISTINCT FROM n AND NOT COALESCE(
      o.state='running' AND n.state='running' AND o.deadline>statement_timestamp() AND n.deadline>=o.deadline
      AND (to_jsonb(o)-'deadline')=(to_jsonb(n)-'deadline'),false))
 ) THEN
  UPDATE console_management_view_epoch SET revision=revision+1,last_xid=pg_current_xact_id()
   WHERE relation_name='console_model_fingerprints' AND last_xid IS DISTINCT FROM pg_current_xact_id();
 END IF;
 RETURN NULL;
END $$;

DROP TRIGGER console_model_fingerprints_view_update ON console_model_fingerprints;
CREATE TRIGGER console_model_fingerprints_view_update AFTER UPDATE ON console_model_fingerprints
 REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT
 EXECUTE FUNCTION console_management_fingerprint_epoch_update_v1();
