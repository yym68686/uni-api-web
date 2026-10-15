-- No historical result reads/backfill while schema locks are held. A bounded
-- background worker populates legacy rows; readers fall back for missing rows.
CREATE TABLE IF NOT EXISTS console_sub_model_summaries (
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL, model TEXT NOT NULL,
 result JSONB NOT NULL,
 PRIMARY KEY(account_id,group_id,model),
 FOREIGN KEY(account_id,group_id,model) REFERENCES console_sub_models(account_id,group_id,model) ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE FUNCTION console_sub_probe_summary_v1(p JSONB) RETURNS JSONB
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE out JSONB; u JSONB; message TEXT;
BEGIN
 IF jsonb_typeof(p) IS DISTINCT FROM 'object' THEN RETURN p; END IF;
 SELECT COALESCE(jsonb_object_agg(key,value),'{}'::jsonb) INTO out
 FROM jsonb_each(p) WHERE key=ANY(ARRAY['id','requested_model','response_model','model_match','status','terminal_status','message','ttft_ms','response_created_ms','first_response_ms','protocol','duration_ms','http_status','usage']);
 IF jsonb_typeof(out->'usage')='object' THEN
  SELECT COALESCE(jsonb_object_agg(key,value),'{}'::jsonb) INTO u
  FROM jsonb_each(out->'usage') WHERE key=ANY(ARRAY['status','input_price','output_price','input_tokens','output_tokens']);
  out:=jsonb_set(out,'{usage}',u);
 END IF;
 IF jsonb_typeof(out->'message')='string' THEN
  message:=out->>'message';
  IF char_length(message)>160 THEN out:=jsonb_set(out,'{message}',to_jsonb(left(message,160)||'…')); END IF;
 END IF;
 RETURN out;
END $$;

CREATE FUNCTION console_sub_model_summary_v1(r JSONB) RETURNS JSONB
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE out JSONB; probe TEXT;
BEGIN
 IF r IS NULL THEN RETURN 'null'::jsonb; END IF;
 IF jsonb_typeof(r) IS DISTINCT FROM 'object' THEN RETURN r; END IF;
 SELECT COALESCE(jsonb_object_agg(key,value),'{}'::jsonb) INTO out
 FROM jsonb_each(r) WHERE key=ANY(ARRAY['model','checked_at','verdict','availability','quality']);
 FOREACH probe IN ARRAY ARRAY['availability','quality'] LOOP
  IF out ? probe THEN out:=jsonb_set(out,ARRAY[probe],console_sub_probe_summary_v1(out->probe)); END IF;
 END LOOP;
 RETURN out;
END $$;

-- Old and new application replicas both write the source of truth. Keep its
-- projection atomic even when an older replica knows nothing about this table.
CREATE FUNCTION console_sub_model_summary_write_v1() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO console_sub_model_summaries(account_id,group_id,model,result)
 VALUES(NEW.account_id,NEW.group_id,NEW.model,console_sub_model_summary_v1(NEW.result))
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET result=EXCLUDED.result;
 RETURN NEW;
END $$;
CREATE TRIGGER console_sub_model_summary_write_v1
 AFTER INSERT OR UPDATE OF result ON console_sub_models
 FOR EACH ROW EXECUTE FUNCTION console_sub_model_summary_write_v1();
