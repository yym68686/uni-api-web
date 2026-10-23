-- Add only empty projections and writer triggers at startup. Preparation is
-- explicit and bounded; readers remain on the legacy path until verified.
-- Independent projections avoid cross-writer receipt/model reference races.
CREATE TABLE console_management_receipt_signal (
 id TEXT PRIMARY KEY REFERENCES console_sub_usage(id) ON DELETE CASCADE ON UPDATE CASCADE,
 account_id TEXT NOT NULL,
 valid BOOLEAN NOT NULL,evidence_digest BYTEA NOT NULL,
 status TEXT NOT NULL,
 input_price DOUBLE PRECISION, output_price DOUBLE PRECISION,
 input_tokens BIGINT, output_tokens BIGINT
);
CREATE TABLE console_management_model_signal (
 account_id TEXT NOT NULL,group_id BIGINT NOT NULL,model TEXT NOT NULL,
 valid BOOLEAN NOT NULL,present BOOLEAN NOT NULL,evidence_digest BYTEA NOT NULL,
 checked_at BIGINT NOT NULL,verdict TEXT NOT NULL,
 availability TEXT NOT NULL,terminal TEXT NOT NULL,model_match TEXT NOT NULL,
 probe_id TEXT NOT NULL,quality_probe_id TEXT NOT NULL,has_usage BOOLEAN NOT NULL,
 usage_status TEXT NOT NULL,input_price DOUBLE PRECISION,output_price DOUBLE PRECISION,
 input_tokens BIGINT,output_tokens BIGINT,
 PRIMARY KEY(account_id,group_id,model),
 FOREIGN KEY(account_id,group_id,model) REFERENCES console_sub_models(account_id,group_id,model) ON DELETE CASCADE ON UPDATE CASCADE
);

-- Conservative fast-path support, not an alternate JSON decoder. Unrecognized
-- fields/casing or numeric representations force the legacy typed reader. This
-- preserves its full error contract without pretending partial evidence is valid.
CREATE FUNCTION console_management_json_fields_v1(r JSONB, text_fields TEXT[], int_fields TEXT[], float_fields TEXT[], object_fields TEXT[]) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE k TEXT;v JSONB;
BEGIN
 IF r IS NULL OR r='null'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(r)<>'object' THEN RETURN false; END IF;
 FOR k,v IN SELECT key,value FROM jsonb_each(r) LOOP
  IF k=ANY(text_fields) THEN
   IF v<>'null'::jsonb AND jsonb_typeof(v)<>'string' THEN RETURN false;END IF;
  ELSIF k=ANY(int_fields) THEN
   IF v<>'null'::jsonb THEN
    IF jsonb_typeof(v)<>'number' THEN RETURN false;END IF;
    PERFORM (v#>>'{}')::bigint;
   END IF;
  ELSIF k=ANY(float_fields) THEN
   IF v<>'null'::jsonb THEN
    IF jsonb_typeof(v)<>'number' THEN RETURN false;END IF;
    PERFORM (v#>>'{}')::double precision;
   END IF;
  ELSIF k=ANY(object_fields) THEN
   IF v<>'null'::jsonb AND jsonb_typeof(v)<>'object' THEN RETURN false;END IF;
  ELSE RETURN false;
  END IF;
 END LOOP;
 RETURN true;
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;
CREATE FUNCTION console_management_usage_supported_v1(r JSONB) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT console_management_json_fields_v1(r,
 ARRAY['status','message','request_id','created_at'],
 ARRAY['log_id','checked_at','input_tokens','output_tokens','cache_read_tokens','cache_creation_tokens','duration_ms','first_token_ms'],
 ARRAY['actual_cost','total_cost','rate_multiplier','input_price','output_price','cache_read_price','cache_write_price','paid_input_price','paid_output_price'],ARRAY[]::text[])
$$;
CREATE FUNCTION console_management_probe_supported_v1(p JSONB) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT console_management_json_fields_v1(p,
 ARRAY['id','requested_model','response_model','model_match','status','terminal_status','message','protocol'],
 ARRAY['ttft_ms','response_created_ms','first_response_ms','duration_ms','http_status'],ARRAY[]::text[],ARRAY['usage'])
 AND console_management_usage_supported_v1(p->'usage')
$$;
CREATE FUNCTION console_management_model_supported_v1(r JSONB) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT console_management_json_fields_v1(r,ARRAY['model','verdict'],ARRAY['checked_at'],ARRAY[]::text[],ARRAY['availability','quality'])
 AND console_management_probe_supported_v1(r->'availability') AND console_management_probe_supported_v1(r->'quality')
$$;

CREATE FUNCTION console_management_receipt_signal_value(r JSONB,base_status TEXT) RETURNS console_management_receipt_signal
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v console_management_receipt_signal; k TEXT;
BEGIN
 v.evidence_digest:=sha256(convert_to(jsonb_build_array(r,base_status)::text,'UTF8'));
 v.valid:=console_management_usage_supported_v1(r);v.status:=base_status;
 IF NOT v.valid THEN RETURN v;END IF;
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
 v.evidence_digest:=sha256(convert_to(jsonb_build_array(v.status,v.input_price,v.output_price,v.input_tokens,v.output_tokens)::text,'UTF8'));
 RETURN v;
EXCEPTION WHEN data_exception THEN
 v.valid:=false;RETURN v;
END $$;

CREATE FUNCTION console_management_model_signal_value(r JSONB) RETURNS console_management_model_signal
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v console_management_model_signal;u console_management_receipt_signal;p JSONB;k TEXT;
BEGIN
 r:=console_sub_model_summary_v1(r);
 v.evidence_digest:=sha256(convert_to(r::text,'UTF8'));
 v.valid:=console_management_model_supported_v1(r);v.present:=(r IS NOT NULL AND r<>'null'::jsonb);
 v.checked_at:=0;v.verdict:='';v.availability:='';v.terminal:='';v.model_match:='';v.probe_id:='';v.quality_probe_id:='';v.has_usage:=false;v.usage_status:='';
 IF jsonb_typeof(r#>'{availability,id}')='string' THEN v.probe_id:=r#>>'{availability,id}'; END IF;
 IF jsonb_typeof(r#>'{quality,id}')='string' THEN v.quality_probe_id:=r#>>'{quality,id}'; END IF;
 IF NOT v.valid OR NOT v.present THEN RETURN v;END IF;
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
 u:=console_management_receipt_signal_value(p->'usage','');
 v.valid:=v.valid AND u.valid;v.usage_status:=u.status;v.input_price:=u.input_price;v.output_price:=u.output_price;
 v.input_tokens:=u.input_tokens;v.output_tokens:=u.output_tokens;
 RETURN v;
EXCEPTION WHEN data_exception THEN
 v.valid:=false;RETURN v;
END $$;

CREATE FUNCTION console_management_model_signal_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE v console_management_model_signal;
BEGIN
 v:=console_management_model_signal_value(NEW.result);
 v.account_id:=NEW.account_id;v.group_id:=NEW.group_id;v.model:=NEW.model;
 INSERT INTO console_management_model_signal SELECT v.*
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET
 valid=EXCLUDED.valid,present=EXCLUDED.present,evidence_digest=EXCLUDED.evidence_digest,checked_at=EXCLUDED.checked_at,verdict=EXCLUDED.verdict,
 availability=EXCLUDED.availability,terminal=EXCLUDED.terminal,model_match=EXCLUDED.model_match,
 probe_id=EXCLUDED.probe_id,quality_probe_id=EXCLUDED.quality_probe_id,has_usage=EXCLUDED.has_usage,usage_status=EXCLUDED.usage_status,
 input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens;
 RETURN NEW;
END $$;
CREATE TRIGGER console_management_model_signal_write AFTER INSERT OR UPDATE OF account_id,group_id,model,result ON console_sub_models
 FOR EACH ROW EXECUTE FUNCTION console_management_model_signal_write();

CREATE FUNCTION console_management_receipt_signal_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE v console_management_receipt_signal;
BEGIN
 v:=console_management_receipt_signal_value(NEW.result,NEW.status);v.id:=NEW.id;v.account_id:=NEW.account_id;
 INSERT INTO console_management_receipt_signal SELECT v.*
 ON CONFLICT(id) DO UPDATE SET account_id=EXCLUDED.account_id,valid=EXCLUDED.valid,evidence_digest=EXCLUDED.evidence_digest,status=EXCLUDED.status,
 input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens;
 RETURN NEW;
END $$;
CREATE TRIGGER console_management_receipt_signal_write AFTER INSERT OR UPDATE OF id,result,status,account_id ON console_sub_usage
 FOR EACH ROW EXECUTE FUNCTION console_management_receipt_signal_write();

-- A separate cursor for each source supports bounded restartable preparation.
-- No historical count or scan is part of startup. Normal reads never set ready.
CREATE TABLE console_management_projection_state (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 phase TEXT NOT NULL DEFAULT 'models' CHECK(phase IN ('models','receipts','verify_models','verify_receipts','complete')),
 started BOOLEAN NOT NULL DEFAULT false,
 cursor_account TEXT NOT NULL DEFAULT '',cursor_group BIGINT NOT NULL DEFAULT 0,cursor_model TEXT NOT NULL DEFAULT '',cursor_receipt TEXT NOT NULL DEFAULT '',
 complete BOOLEAN NOT NULL DEFAULT false,
 CHECK(complete=(phase='complete'))
);
INSERT INTO console_management_projection_state(singleton) VALUES(true);
