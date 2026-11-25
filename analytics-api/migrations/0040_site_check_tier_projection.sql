CREATE OR REPLACE FUNCTION console_sub_model_summary_v1(r JSONB) RETURNS JSONB
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE out JSONB; probe TEXT;
BEGIN
 IF r IS NULL THEN RETURN 'null'::jsonb; END IF;
 IF jsonb_typeof(r) IS DISTINCT FROM 'object' THEN RETURN r; END IF;
 SELECT COALESCE(jsonb_object_agg(key,value),'{}'::jsonb) INTO out
 FROM jsonb_each(r) WHERE key=ANY(ARRAY['model','checked_at','verdict','availability','quality','service_tiers']);
 FOREACH probe IN ARRAY ARRAY['availability','quality'] LOOP
  IF out ? probe THEN out:=jsonb_set(out,ARRAY[probe],console_sub_probe_summary_v1(out->probe)); END IF;
 END LOOP;
 RETURN out;
END $$;
ALTER TABLE console_sub_models ADD COLUMN IF NOT EXISTS tier_read_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS tool_read_version BIGINT NOT NULL DEFAULT 0;

-- A site's result belongs to its test credential, never its routing key.
CREATE FUNCTION console_clear_site_tier_evidence_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 UPDATE console_sub_models SET result=result-'service_tiers',tier_read_version=tier_read_version+1
 WHERE account_id=NEW.account_id AND group_id=NEW.group_id AND result ? 'service_tiers';
 RETURN NEW;
END $$;
CREATE TRIGGER console_clear_site_tier_evidence_v1
 AFTER UPDATE OF encrypted_key,remote_key_id ON console_sub_targets
 FOR EACH ROW WHEN (OLD.encrypted_key IS DISTINCT FROM NEW.encrypted_key OR OLD.remote_key_id IS DISTINCT FROM NEW.remote_key_id)
 EXECUTE FUNCTION console_clear_site_tier_evidence_v1();
