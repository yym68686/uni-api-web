-- A nullable column adds no table rewrite; legacy values are repaired in
-- bounded transactions by maintenance, not while startup holds schema locks.
ALTER TABLE console_management_model_signal ADD COLUMN service_tiers JSONB;

CREATE OR REPLACE FUNCTION console_management_model_supported_v1(r JSONB) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT console_management_json_fields_v1(r,ARRAY['model','verdict'],ARRAY['checked_at'],ARRAY[]::text[],ARRAY['availability','quality','service_tiers'])
 AND console_management_probe_supported_v1(r->'availability')
 AND console_management_probe_supported_v1(r->'quality')
 AND NOT EXISTS (
  SELECT 1 FROM jsonb_each(CASE WHEN jsonb_typeof(r->'service_tiers')='object' THEN r->'service_tiers' ELSE '{}'::jsonb END) t
  WHERE NOT console_management_json_fields_v1(t.value,ARRAY['status','observed'],ARRAY['checked_at'],ARRAY[]::text[],ARRAY[]::text[])
 )
$$;

CREATE OR REPLACE FUNCTION console_management_model_statement_write_v1() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO console_management_model_signal(account_id,group_id,model,valid,present,evidence_digest,checked_at,verdict,
  availability,terminal,model_match,probe_id,quality_probe_id,has_usage,usage_status,input_price,output_price,input_tokens,output_tokens,service_tiers)
 SELECT n.account_id,n.group_id,n.model,v.valid,v.present,v.evidence_digest,v.checked_at,v.verdict,
  v.availability,v.terminal,v.model_match,v.probe_id,v.quality_probe_id,v.has_usage,v.usage_status,v.input_price,v.output_price,
  v.input_tokens,v.output_tokens,CASE WHEN v.valid THEN NULLIF(n.result->'service_tiers','null'::jsonb) END
 FROM new_rows n CROSS JOIN LATERAL console_management_model_signal_value(n.result) v
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET
  valid=EXCLUDED.valid,present=EXCLUDED.present,evidence_digest=EXCLUDED.evidence_digest,checked_at=EXCLUDED.checked_at,verdict=EXCLUDED.verdict,
  availability=EXCLUDED.availability,terminal=EXCLUDED.terminal,model_match=EXCLUDED.model_match,
  probe_id=EXCLUDED.probe_id,quality_probe_id=EXCLUDED.quality_probe_id,has_usage=EXCLUDED.has_usage,usage_status=EXCLUDED.usage_status,
  input_price=EXCLUDED.input_price,output_price=EXCLUDED.output_price,input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens,
  service_tiers=EXCLUDED.service_tiers
 WHERE console_management_model_signal IS DISTINCT FROM EXCLUDED;
 RETURN NULL;
END $$;
