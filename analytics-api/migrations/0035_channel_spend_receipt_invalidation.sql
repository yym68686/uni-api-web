-- Receipt corrections and their cache invalidation commit together, including
-- pages written by an older replica during rollout. Control-only installs do
-- not have the analytics schema and leave the optional cache disabled.
CREATE FUNCTION console_spend_receipt_invalidate_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE keys text[];
BEGIN
 IF to_regprocedure('analytics.channel_spend_invalidate_receipts_v1(text[],text[])') IS NULL THEN RETURN NULL; END IF;
 IF TG_OP='INSERT' THEN
   SELECT array_agg(DISTINCT request_id) INTO keys FROM new_spend_receipts WHERE request_id<>'';
 ELSIF TG_OP='DELETE' THEN
   SELECT array_agg(DISTINCT request_id) INTO keys FROM old_spend_receipts WHERE request_id<>'';
 ELSE
   SELECT array_agg(DISTINCT k.request_id) INTO keys
   FROM new_spend_receipts n FULL JOIN old_spend_receipts o USING(account_id,key_id,log_id)
   CROSS JOIN LATERAL (VALUES(n.request_id),(o.request_id)) k(request_id)
   WHERE k.request_id<>'' AND (n.request_id,n.actual_cost,n.at_ms) IS DISTINCT FROM (o.request_id,o.actual_cost,o.at_ms);
 END IF;
 PERFORM analytics.channel_spend_invalidate_receipts_v1(keys,NULL);
 RETURN NULL;
END $$;
CREATE TRIGGER console_spend_receipts_insert_v1 AFTER INSERT ON console_sub_spend_logs
 REFERENCING NEW TABLE AS new_spend_receipts FOR EACH STATEMENT EXECUTE FUNCTION console_spend_receipt_invalidate_v1();
CREATE TRIGGER console_spend_receipts_update_v1 AFTER UPDATE ON console_sub_spend_logs
 REFERENCING OLD TABLE AS old_spend_receipts NEW TABLE AS new_spend_receipts FOR EACH STATEMENT EXECUTE FUNCTION console_spend_receipt_invalidate_v1();
CREATE TRIGGER console_spend_receipts_delete_v1 AFTER DELETE ON console_sub_spend_logs
 REFERENCING OLD TABLE AS old_spend_receipts FOR EACH STATEMENT EXECUTE FUNCTION console_spend_receipt_invalidate_v1();

CREATE FUNCTION console_spend_evidence_invalidate_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE events text[];
BEGIN
 IF to_regprocedure('analytics.channel_spend_invalidate_receipts_v1(text[],text[])') IS NULL THEN RETURN NULL; END IF;
 IF TG_OP='INSERT' THEN
   SELECT array_agg(DISTINCT event_id) INTO events FROM new_spend_evidence;
 ELSIF TG_OP='DELETE' THEN
   SELECT array_agg(DISTINCT event_id) INTO events FROM old_spend_evidence;
 ELSE
   SELECT array_agg(DISTINCT event_id) INTO events FROM
   (SELECT event_id FROM new_spend_evidence UNION SELECT event_id FROM old_spend_evidence) changed;
 END IF;
 PERFORM analytics.channel_spend_invalidate_receipts_v1(NULL,events);
 RETURN NULL;
END $$;
CREATE TRIGGER console_spend_evidence_insert_v1 AFTER INSERT ON console_billing_error_evidence
 REFERENCING NEW TABLE AS new_spend_evidence FOR EACH STATEMENT EXECUTE FUNCTION console_spend_evidence_invalidate_v1();
CREATE TRIGGER console_spend_evidence_update_v1 AFTER UPDATE ON console_billing_error_evidence
 REFERENCING OLD TABLE AS old_spend_evidence NEW TABLE AS new_spend_evidence FOR EACH STATEMENT EXECUTE FUNCTION console_spend_evidence_invalidate_v1();
CREATE TRIGGER console_spend_evidence_delete_v1 AFTER DELETE ON console_billing_error_evidence
 REFERENCING OLD TABLE AS old_spend_evidence FOR EACH STATEMENT EXECUTE FUNCTION console_spend_evidence_invalidate_v1();

CREATE FUNCTION console_spend_truncate_invalidate_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF to_regclass('analytics.channel_spend_slices') IS NOT NULL THEN
   UPDATE analytics.channel_spend_epoch SET epoch=epoch+1 WHERE singleton;
   DELETE FROM analytics.channel_spend_slices;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER console_spend_receipts_truncate_v1 AFTER TRUNCATE ON console_sub_spend_logs
 FOR EACH STATEMENT EXECUTE FUNCTION console_spend_truncate_invalidate_v1();
CREATE TRIGGER console_spend_evidence_truncate_v1 AFTER TRUNCATE ON console_billing_error_evidence
 FOR EACH STATEMENT EXECUTE FUNCTION console_spend_truncate_invalidate_v1();
