-- Counters are changed in the same transaction as their source relations.
-- Startup installs only empty baseline metadata; explicit preparation measures
-- the existing counts in repeatable-read snapshots before enabling compact reads.
CREATE TABLE console_management_revision_state(
 relation_name TEXT PRIMARY KEY,revision BIGINT NOT NULL DEFAULT 0,
 rows_delta BIGINT NOT NULL DEFAULT 0,invalid_delta BIGINT NOT NULL DEFAULT 0,
 rows_base BIGINT NOT NULL DEFAULT 0,invalid_base BIGINT NOT NULL DEFAULT 0,
 complete BOOLEAN NOT NULL DEFAULT false
);
INSERT INTO console_management_revision_state(relation_name) VALUES
 ('console_sub_models'),('console_management_model_signal'),('console_sub_usage'),('console_management_receipt_signal');
CREATE FUNCTION console_management_revision_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE added BIGINT:=0;removed BIGINT:=0;bad_added BIGINT:=0;bad_removed BIGINT:=0;changed BOOLEAN:=true;columns_sql TEXT:=TG_ARGV[0];
BEGIN
 IF TG_OP='TRUNCATE' THEN
  EXECUTE format('UPDATE %I.console_management_revision_state SET revision=revision+1,rows_delta=0,invalid_delta=0,rows_base=0,invalid_base=0,complete=true WHERE relation_name=$1',TG_TABLE_SCHEMA) USING TG_TABLE_NAME;
  RETURN NULL;
 END IF;
 IF TG_OP<>'DELETE' THEN
  SELECT count(*) INTO added FROM new_rows;
  IF TG_ARGV[1]='true' THEN SELECT count(*) INTO bad_added FROM new_rows WHERE NOT valid; END IF;
 END IF;
 IF TG_OP<>'INSERT' THEN
  SELECT count(*) INTO removed FROM old_rows;
  IF TG_ARGV[1]='true' THEN SELECT count(*) INTO bad_removed FROM old_rows WHERE NOT valid; END IF;
 END IF;
 IF added=0 AND removed=0 THEN RETURN NULL; END IF;
 IF TG_OP='UPDATE' THEN
  EXECUTE 'SELECT EXISTS((SELECT '||columns_sql||' FROM new_rows EXCEPT ALL SELECT '||columns_sql||' FROM old_rows) UNION ALL (SELECT '||columns_sql||' FROM old_rows EXCEPT ALL SELECT '||columns_sql||' FROM new_rows))' INTO changed;
 END IF;
 IF changed THEN
  EXECUTE format('UPDATE %I.console_management_revision_state SET revision=revision+1,rows_delta=rows_delta+$1,invalid_delta=invalid_delta+$2 WHERE relation_name=$3',TG_TABLE_SCHEMA) USING added-removed,bad_added-bad_removed,TG_TABLE_NAME;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER console_sub_models_revision_insert AFTER INSERT ON console_sub_models REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('account_id,group_id,model,state,message','false');
CREATE TRIGGER console_sub_models_revision_update AFTER UPDATE ON console_sub_models REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('account_id,group_id,model,state,message','false');
CREATE TRIGGER console_sub_models_revision_delete AFTER DELETE ON console_sub_models REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('account_id,group_id,model,state,message','false');
CREATE TRIGGER console_management_model_signal_revision_insert AFTER INSERT ON console_management_model_signal REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_management_model_signal_revision_update AFTER UPDATE ON console_management_model_signal REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_management_model_signal_revision_delete AFTER DELETE ON console_management_model_signal REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_sub_usage_revision_insert AFTER INSERT ON console_sub_usage REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('id,account_id','false');
CREATE TRIGGER console_sub_usage_revision_update AFTER UPDATE ON console_sub_usage REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('id,account_id','false');
CREATE TRIGGER console_sub_usage_revision_delete AFTER DELETE ON console_sub_usage REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('id,account_id','false');
CREATE TRIGGER console_management_receipt_signal_revision_insert AFTER INSERT ON console_management_receipt_signal REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_management_receipt_signal_revision_update AFTER UPDATE ON console_management_receipt_signal REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_management_receipt_signal_revision_delete AFTER DELETE ON console_management_receipt_signal REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('*','true');
CREATE TRIGGER console_sub_models_revision_truncate AFTER TRUNCATE ON console_sub_models FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('','false');
CREATE TRIGGER console_management_model_signal_revision_truncate AFTER TRUNCATE ON console_management_model_signal FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('','false');
CREATE TRIGGER console_sub_usage_revision_truncate AFTER TRUNCATE ON console_sub_usage FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('','false');
CREATE TRIGGER console_management_receipt_signal_revision_truncate AFTER TRUNCATE ON console_management_receipt_signal FOR EACH STATEMENT EXECUTE FUNCTION console_management_revision_write_v1('','false');

-- Maintain projections in statement batches, so a multi-row source write
-- advances the current revision once instead of rewriting its counter per row.
CREATE FUNCTION console_management_model_statement_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO console_management_model_signal(account_id,group_id,model,valid,present,evidence_digest,checked_at,verdict,availability,terminal,model_match,probe_id,quality_probe_id,has_usage,usage_status,input_price,output_price,input_tokens,output_tokens) SELECT n.account_id,n.group_id,n.model,v.valid,v.present,v.evidence_digest,v.checked_at,v.verdict,v.availability,v.terminal,v.model_match,v.probe_id,v.quality_probe_id,v.has_usage,v.usage_status,v.input_price,v.output_price,v.input_tokens,v.output_tokens FROM new_rows n CROSS JOIN LATERAL console_management_model_signal_value(n.result) v ON CONFLICT(account_id,group_id,model) DO UPDATE SET valid=EXCLUDED.valid,present=EXCLUDED.present,evidence_digest=EXCLUDED.evidence_digest,checked_at=EXCLUDED.checked_at,verdict=EXCLUDED.verdict,availability=EXCLUDED.availability,terminal=EXCLUDED.terminal,model_match=EXCLUDED.model_match,probe_id=EXCLUDED.probe_id,quality_probe_id=EXCLUDED.quality_probe_id,has_usage=EXCLUDED.has_usage,usage_status=EXCLUDED.usage_status,input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens WHERE console_management_model_signal IS DISTINCT FROM EXCLUDED;

 RETURN NULL;
END $$;
DROP TRIGGER console_management_model_signal_write ON console_sub_models;
CREATE TRIGGER console_management_model_signal_insert AFTER INSERT ON console_sub_models REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_model_statement_write_v1();
CREATE TRIGGER console_management_model_signal_update AFTER UPDATE ON console_sub_models REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_model_statement_write_v1();
CREATE FUNCTION console_management_receipt_statement_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO console_management_receipt_signal(id,account_id,valid,evidence_digest,status,input_price,output_price,input_tokens,output_tokens,rate_result) SELECT n.id,n.account_id,v.valid,v.evidence_digest,v.status,v.input_price,v.output_price,v.input_tokens,v.output_tokens,v.rate_result FROM new_rows n CROSS JOIN LATERAL console_management_receipt_signal_value(n.result,n.status) v ON CONFLICT(id) DO UPDATE SET account_id=EXCLUDED.account_id,valid=EXCLUDED.valid,evidence_digest=EXCLUDED.evidence_digest,status=EXCLUDED.status,input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens,rate_result=EXCLUDED.rate_result WHERE console_management_receipt_signal IS DISTINCT FROM EXCLUDED;

 RETURN NULL;
END $$;
DROP TRIGGER console_management_receipt_signal_write ON console_sub_usage;
CREATE TRIGGER console_management_receipt_signal_insert AFTER INSERT ON console_sub_usage REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_receipt_statement_write_v1();
CREATE TRIGGER console_management_receipt_signal_update AFTER UPDATE ON console_sub_usage REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION console_management_receipt_statement_write_v1();
