-- Latest billing evidence is channel metadata, not disposable request history.
-- Keep only one compact receipt per current group/key/model. Existing history
-- is still read as a fallback; no historical scan runs on the startup path.
CREATE TABLE console_sub_observed_rates (
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL, model TEXT NOT NULL,
 key_id BIGINT NOT NULL, started_at BIGINT NOT NULL, receipt_id TEXT NOT NULL,
 result JSONB NOT NULL,
 PRIMARY KEY(account_id,group_id,model),
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE
);

CREATE FUNCTION console_sub_observed_rate_write_v1() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE receipt console_sub_usage%ROWTYPE; compact JSONB;
BEGIN
 IF TG_OP='DELETE' THEN receipt:=OLD; ELSE receipt:=NEW; END IF;
 IF receipt.status='matched' THEN
  SELECT COALESCE(jsonb_object_agg(key,value),'{}'::jsonb) INTO compact
  FROM jsonb_each(CASE WHEN jsonb_typeof(receipt.result)='object' THEN receipt.result ELSE '{}'::jsonb END)
  WHERE key=ANY(ARRAY['status','actual_cost','total_cost','rate_multiplier','input_price','output_price','created_at']);
  INSERT INTO console_sub_observed_rates(account_id,group_id,model,key_id,started_at,receipt_id,result)
  SELECT receipt.account_id,receipt.group_id,receipt.model,receipt.key_id,receipt.started_at,receipt.id,compact
  FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id
  WHERE t.account_id=receipt.account_id AND t.group_id=receipt.group_id AND t.remote_key_id=receipt.key_id
  ON CONFLICT(account_id,group_id,model) DO UPDATE SET
   key_id=EXCLUDED.key_id,started_at=EXCLUDED.started_at,receipt_id=EXCLUDED.receipt_id,result=EXCLUDED.result
  WHERE console_sub_observed_rates.key_id<>EXCLUDED.key_id
   OR (console_sub_observed_rates.started_at,console_sub_observed_rates.receipt_id)<=(EXCLUDED.started_at,EXCLUDED.receipt_id);
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

-- Database triggers also cover an old API writer/retention worker during
-- rollout or code rollback. Request removal and metadata publication are atomic.
CREATE TRIGGER console_sub_observed_rate_write_v1
 AFTER INSERT OR UPDATE OF status,result ON console_sub_usage
 FOR EACH ROW EXECUTE FUNCTION console_sub_observed_rate_write_v1();
CREATE TRIGGER console_sub_observed_rate_retain_v1
 BEFORE DELETE ON console_sub_usage
 FOR EACH ROW EXECUTE FUNCTION console_sub_observed_rate_write_v1();
