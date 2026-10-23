-- Pure functions only: no stored data, writer changes or historical backfill.
-- Unsupported representations keep the original JSON for Go's full decoder.
CREATE FUNCTION console_sub_complete_probe_supported_v1(p JSONB) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v JSONB;
BEGIN
 IF p IS NULL OR p='null'::jsonb THEN RETURN true; END IF;
 IF NOT console_management_json_fields_v1(p-'request_ids',
 ARRAY['curl_token','id','requested_model','response_model','model_match','status','terminal_status','text','message','protocol'],
 ARRAY['started_at','ttft_ms','response_created_ms','first_response_ms','duration_ms','http_status'],ARRAY[]::text[],ARRAY['usage'])
 OR NOT console_management_usage_supported_v1(p->'usage') THEN RETURN false; END IF;
 IF p ? 'request_ids' AND p->'request_ids'<>'null'::jsonb THEN
  IF jsonb_typeof(p->'request_ids')<>'array' THEN RETURN false; END IF;
  FOR v IN SELECT value FROM jsonb_array_elements(p->'request_ids') LOOP
   IF jsonb_typeof(v) NOT IN ('string','null') THEN RETURN false; END IF;
  END LOOP;
 END IF;
 RETURN true;
END $$;

CREATE FUNCTION console_sub_capability_supported_v1(r JSONB,depth INTEGER DEFAULT 0) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v JSONB;
BEGIN
 IF r IS NULL OR r='null'::jsonb THEN RETURN true; END IF;
 IF depth>32 OR jsonb_typeof(r)<>'object' THEN RETURN false; END IF;
 IF NOT console_management_json_fields_v1(r-'models'-'attempts',ARRAY['status','model','message'],ARRAY['checked_at'],ARRAY[]::text[],ARRAY[]::text[]) THEN RETURN false; END IF;
 IF r ? 'attempts' AND r->'attempts'<>'null'::jsonb THEN
  IF jsonb_typeof(r->'attempts')<>'array' THEN RETURN false; END IF;
  FOR v IN SELECT value FROM jsonb_array_elements(r->'attempts') LOOP
   IF NOT console_sub_complete_probe_supported_v1(v) THEN RETURN false; END IF;
  END LOOP;
 END IF;
 IF r ? 'models' AND r->'models'<>'null'::jsonb THEN
  IF jsonb_typeof(r->'models')<>'array' THEN RETURN false; END IF;
  FOR v IN SELECT value FROM jsonb_array_elements(r->'models') LOOP
   IF NOT console_management_json_fields_v1(v,ARRAY['model','state'],ARRAY[]::text[],ARRAY[]::text[],ARRAY['result'])
   OR NOT console_sub_capability_supported_v1(v->'result',depth+1) THEN RETURN false; END IF;
  END LOOP;
 END IF;
 RETURN true;
END $$;

CREATE FUNCTION console_sub_capability_compact_v1(r JSONB) RETURNS JSONB
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE out JSONB;models JSONB;v JSONB;
BEGIN
 IF r IS NULL OR r='null'::jsonb THEN RETURN r; END IF;
 out:=jsonb_set(r,'{attempts}','[]'::jsonb);
 IF jsonb_typeof(out->'message')='string' AND char_length(out->>'message')>160 THEN
  out:=jsonb_set(out,'{message}',to_jsonb(left(out->>'message',160)||'…'));
 END IF;
 IF jsonb_typeof(out->'models')='array' THEN
  models:='[]'::jsonb;
  FOR v IN SELECT value FROM jsonb_array_elements(out->'models') LOOP
   IF v ? 'result' THEN v:=jsonb_set(v,'{result}',console_sub_capability_compact_v1(v->'result')); END IF;
   models:=models||jsonb_build_array(v);
  END LOOP;
  out:=jsonb_set(out,'{models}',models);
 END IF;
 RETURN out;
END $$;

CREATE FUNCTION console_sub_capability_summary_v1(r JSONB) RETURNS JSONB
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN console_sub_capability_supported_v1(r) THEN console_sub_capability_compact_v1(r) ELSE r END
$$;
