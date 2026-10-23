-- Isolated prototype only. Not registered in controlMigrations and never
-- applied by startup. Model and receipt projections are independent, so a
-- concurrent receipt update cannot miss a newly published model reference.
CREATE TABLE management_receipt_signal (
 id TEXT PRIMARY KEY REFERENCES console_sub_usage(id) ON DELETE CASCADE ON UPDATE CASCADE,
 account_id TEXT NOT NULL,
 valid BOOLEAN NOT NULL,
 status TEXT NOT NULL,
 input_price DOUBLE PRECISION, output_price DOUBLE PRECISION,
 input_tokens BIGINT, output_tokens BIGINT
);
CREATE TABLE management_model_signal (
 account_id TEXT NOT NULL,group_id BIGINT NOT NULL,model TEXT NOT NULL,
 valid BOOLEAN NOT NULL,present BOOLEAN NOT NULL,
 checked_at BIGINT NOT NULL,verdict TEXT NOT NULL,
 availability TEXT NOT NULL,terminal TEXT NOT NULL,model_match TEXT NOT NULL,
 probe_id TEXT NOT NULL,has_usage BOOLEAN NOT NULL,
 usage_status TEXT NOT NULL,input_price DOUBLE PRECISION,output_price DOUBLE PRECISION,
 input_tokens BIGINT,output_tokens BIGINT,
 PRIMARY KEY(account_id,group_id,model),
 FOREIGN KEY(account_id,group_id,model) REFERENCES console_sub_models(account_id,group_id,model) ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE FUNCTION management_receipt_signal_value(r JSONB,base_status TEXT) RETURNS management_receipt_signal
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v management_receipt_signal; k TEXT;
BEGIN
 v.valid:=true;v.status:=base_status;
 IF r IS NULL OR r='null'::jsonb THEN RETURN v; END IF;
 IF jsonb_typeof(r)<>'object' THEN v.valid:=false;RETURN v;END IF;
 IF r->'status' IS NOT NULL AND r->'status'<>'null'::jsonb THEN
  IF jsonb_typeof(r->'status')<>'string' THEN v.valid:=false;RETURN v;END IF;
  v.status:=r->>'status';
 END IF;
 FOREACH k IN ARRAY ARRAY['input_price','output_price','input_tokens','output_tokens'] LOOP
  IF r->k IS NOT NULL AND r->k<>'null'::jsonb AND jsonb_typeof(r->k)<>'number' THEN v.valid:=false;RETURN v;END IF;
 END LOOP;
 v.input_price:=(r->>'input_price')::double precision;
 v.output_price:=(r->>'output_price')::double precision;
 v.input_tokens:=(r->>'input_tokens')::bigint;
 v.output_tokens:=(r->>'output_tokens')::bigint;
 RETURN v;
EXCEPTION WHEN data_exception THEN
 v.valid:=false;RETURN v;
END $$;

CREATE FUNCTION management_model_signal_value(r JSONB) RETURNS management_model_signal
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v management_model_signal;u management_receipt_signal;p JSONB;k TEXT;
BEGIN
 v.valid:=true;v.present:=(r IS NOT NULL AND r<>'null'::jsonb);
 v.checked_at:=0;v.verdict:='';v.availability:='';v.terminal:='';v.model_match:='';v.probe_id:='';v.has_usage:=false;v.usage_status:='';
 IF NOT v.present THEN RETURN v;END IF;
 IF jsonb_typeof(r)<>'object' THEN v.valid:=false;RETURN v;END IF;
 IF r->'checked_at' IS NOT NULL AND r->'checked_at'<>'null'::jsonb AND jsonb_typeof(r->'checked_at')<>'number' THEN v.valid:=false;RETURN v;END IF;
 IF r->'verdict' IS NOT NULL AND r->'verdict'<>'null'::jsonb AND jsonb_typeof(r->'verdict')<>'string' THEN v.valid:=false;RETURN v;END IF;
 v.checked_at:=COALESCE((r->>'checked_at')::bigint,0);v.verdict:=COALESCE(r->>'verdict','');
 p:=r->'availability';
 IF p IS NULL OR p='null'::jsonb THEN RETURN v;END IF;
 IF jsonb_typeof(p)<>'object' THEN v.valid:=false;RETURN v;END IF;
 FOREACH k IN ARRAY ARRAY['status','terminal_status','model_match','id'] LOOP
  IF p->k IS NOT NULL AND p->k<>'null'::jsonb AND jsonb_typeof(p->k)<>'string' THEN v.valid:=false;RETURN v;END IF;
 END LOOP;
 v.availability:=COALESCE(p->>'status','');v.terminal:=COALESCE(p->>'terminal_status','');
 v.model_match:=COALESCE(p->>'model_match','');v.probe_id:=COALESCE(p->>'id','');
 v.has_usage:=p->'usage' IS NOT NULL AND p->'usage'<>'null'::jsonb;
 u:=management_receipt_signal_value(p->'usage','');
 v.valid:=u.valid;v.usage_status:=u.status;v.input_price:=u.input_price;v.output_price:=u.output_price;
 v.input_tokens:=u.input_tokens;v.output_tokens:=u.output_tokens;
 RETURN v;
EXCEPTION WHEN data_exception THEN
 v.valid:=false;RETURN v;
END $$;

CREATE FUNCTION management_model_signal_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE v management_model_signal;
BEGIN
 v:=management_model_signal_value(NEW.result);
 v.account_id:=NEW.account_id;v.group_id:=NEW.group_id;v.model:=NEW.model;
 INSERT INTO management_model_signal SELECT v.*
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET
 valid=EXCLUDED.valid,present=EXCLUDED.present,checked_at=EXCLUDED.checked_at,verdict=EXCLUDED.verdict,
 availability=EXCLUDED.availability,terminal=EXCLUDED.terminal,model_match=EXCLUDED.model_match,
 probe_id=EXCLUDED.probe_id,has_usage=EXCLUDED.has_usage,usage_status=EXCLUDED.usage_status,
 input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens;
 RETURN NEW;
END $$;
CREATE TRIGGER management_model_signal_write AFTER INSERT OR UPDATE OF result ON console_sub_models
 FOR EACH ROW EXECUTE FUNCTION management_model_signal_write();

CREATE FUNCTION management_receipt_signal_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE v management_receipt_signal;
BEGIN
 v:=management_receipt_signal_value(NEW.result,NEW.status);v.id:=NEW.id;v.account_id:=NEW.account_id;
 INSERT INTO management_receipt_signal SELECT v.*
 ON CONFLICT(id) DO UPDATE SET account_id=EXCLUDED.account_id,valid=EXCLUDED.valid,status=EXCLUDED.status,
 input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens;
 RETURN NEW;
END $$;
CREATE TRIGGER management_receipt_signal_write AFTER INSERT OR UPDATE OF result,status,account_id ON console_sub_usage
 FOR EACH ROW EXECUTE FUNCTION management_receipt_signal_write();
