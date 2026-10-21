CREATE SCHEMA IF NOT EXISTS analytics;
SET search_path=analytics,public;
CREATE TABLE IF NOT EXISTS schema_versions(version TEXT PRIMARY KEY,checksum TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS prices(
 model TEXT PRIMARY KEY,input DOUBLE PRECISION,output DOUBLE PRECISION,
 cache_read DOUBLE PRECISION,cache_write DOUBLE PRECISION,cache_write_1h DOUBLE PRECISION,
 source TEXT,verified BOOLEAN,effective_at TIMESTAMPTZ,
 charge_cache_write BOOLEAN,sale_percent DOUBLE PRECISION,long_context_premium BOOLEAN);
CREATE TABLE IF NOT EXISTS price_history(model TEXT,document TEXT,updated_at TIMESTAMPTZ DEFAULT current_timestamp);
CREATE TABLE IF NOT EXISTS price_migration_conflicts(model TEXT PRIMARY KEY,baseline JSONB,current_price JSONB,legacy_price JSONB,created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp);
CREATE TABLE IF NOT EXISTS meta(name TEXT PRIMARY KEY,value TEXT);
CREATE TABLE IF NOT EXISTS storage_state(
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 revision BIGINT NOT NULL DEFAULT 0,
 evicted_before_ms BIGINT NOT NULL DEFAULT 0,
 receipt_evicted_before_ms BIGINT NOT NULL DEFAULT 0,
 evicted_events BIGINT NOT NULL DEFAULT 0,
 claims_history_complete BOOLEAN NOT NULL DEFAULT true,
 database_limit_bytes BIGINT NOT NULL DEFAULT 10000000000,
 ingest_limit_bytes BIGINT NOT NULL DEFAULT 7500000000,
 reclaim_target_bytes BIGINT NOT NULL DEFAULT 6500000000);
INSERT INTO storage_state(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS fact_partitions(
 start_ms BIGINT PRIMARY KEY,end_ms BIGINT NOT NULL,name TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS fact_source_ranges(source_id TEXT NOT NULL,start_ms BIGINT NOT NULL,first_ms BIGINT NOT NULL,last_ms BIGINT NOT NULL,PRIMARY KEY(source_id,start_ms));
CREATE TABLE IF NOT EXISTS facts(
 event_id TEXT NOT NULL,kind TEXT NOT NULL,source_id TEXT NOT NULL DEFAULT 'primary',
 instance_id TEXT,request_id TEXT,attempt_id TEXT,at_ms BIGINT NOT NULL,started_ms BIGINT,
 key_id TEXT,provider TEXT,model TEXT,upstream_model TEXT,endpoint TEXT,stream BOOLEAN,
 outcome TEXT,status INTEGER,duration_ms DOUBLE PRECISION,dispatch_ms DOUBLE PRECISION,
 first_output_ms DOUBLE PRECISION,response_created_ms DOUBLE PRECISION,first_text_ms DOUBLE PRECISION,
 input_tokens BIGINT,output_tokens BIGINT,cache_read_tokens BIGINT,cache_write_tokens BIGINT,
 cache_write_1h_tokens BIGINT,actual_cost_usd DOUBLE PRECISION,upstream_base TEXT,
 upstream_key_hash TEXT,billing_request_ids TEXT[],upstream_error_sha256 TEXT,
 trace_id TEXT,stage TEXT,trace_detail TEXT,transport_timing TEXT,terminal_kind TEXT,
 failure_reason TEXT,response_completed BOOLEAN,
 PRIMARY KEY(at_ms,event_id)) PARTITION BY RANGE(at_ms);
CREATE INDEX IF NOT EXISTS facts_request ON facts(source_id,request_id);
CREATE INDEX IF NOT EXISTS facts_trace ON facts(source_id,trace_id);
CREATE INDEX IF NOT EXISTS facts_scope ON facts(source_id,key_id,model,at_ms);
CREATE INDEX IF NOT EXISTS facts_event ON facts(event_id);
CREATE TABLE IF NOT EXISTS receipt_claim_history(digest TEXT PRIMARY KEY,claims BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS imported_objects(
 object_key TEXT PRIMARY KEY,etag TEXT NOT NULL,
 imported_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,events BIGINT,
 max_at_ms BIGINT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ingest_sources(
 source_id TEXT PRIMARY KEY,last_received_ms BIGINT NOT NULL,
 first_received_ms BIGINT NOT NULL,last_event_ms BIGINT NOT NULL,
 dropped_events BIGINT NOT NULL DEFAULT 0,gap_after_ms BIGINT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ingest_producers(
 source_id TEXT NOT NULL,epoch TEXT NOT NULL,instance_id TEXT NOT NULL,
 first_received_ms BIGINT NOT NULL,last_received_ms BIGINT NOT NULL,
 pending BIGINT NOT NULL,dropped BIGINT NOT NULL,last_sequence BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(source_id,epoch));
CREATE TABLE IF NOT EXISTS rollups(
 period_ms BIGINT NOT NULL,level TEXT NOT NULL,kind TEXT NOT NULL,source_id TEXT NOT NULL DEFAULT 'primary',key_id TEXT NOT NULL,
 provider TEXT NOT NULL,model TEXT NOT NULL,upstream_model TEXT NOT NULL,endpoint TEXT NOT NULL,stream BOOLEAN NOT NULL,outcome TEXT NOT NULL,
 n BIGINT,input_tokens NUMERIC,output_tokens NUMERIC,cache_read_tokens NUMERIC,
 cache_write_tokens NUMERIC,cache_write_1h_tokens NUMERIC,usage_samples BIGINT,cache_samples BIGINT,
 actual_cost_usd DOUBLE PRECISION,actual_cost_samples BIGINT,first_bins BIGINT[],dispatch_bins BIGINT[],
 first_count BIGINT,dispatch_count BIGINT,first_sum DOUBLE PRECISION,dispatch_sum DOUBLE PRECISION,
 last_ms BIGINT,last_first DOUBLE PRECISION,last_dispatch DOUBLE PRECISION,
 created_bins BIGINT[],created_count BIGINT DEFAULT 0,created_sum DOUBLE PRECISION,last_created DOUBLE PRECISION,
 text_bins BIGINT[],text_count BIGINT DEFAULT 0,text_sum DOUBLE PRECISION,last_text DOUBLE PRECISION,
 last_first_ms BIGINT,last_dispatch_ms BIGINT,last_created_ms BIGINT,last_text_ms BIGINT,
 UNIQUE(period_ms,level,kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome)) PARTITION BY RANGE(period_ms);
CREATE TABLE IF NOT EXISTS rollup_partitions(start_ms BIGINT PRIMARY KEY,end_ms BIGINT NOT NULL,name TEXT UNIQUE NOT NULL);
CREATE INDEX IF NOT EXISTS rollups_window ON rollups(level,period_ms);
CREATE INDEX IF NOT EXISTS rollups_scope ON rollups(source_id,key_id,model,level,period_ms);
-- Exact arg_max semantics: ignore null values, take the last observed clock.
CREATE OR REPLACE FUNCTION last_observed(state DOUBLE PRECISION[],value DOUBLE PRECISION,clock BIGINT)
RETURNS DOUBLE PRECISION[] LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN value IS NOT NULL AND clock IS NOT NULL AND
 (state IS NULL OR clock>=state[2]) THEN ARRAY[value,clock::DOUBLE PRECISION] ELSE state END $$;
CREATE OR REPLACE FUNCTION observed_value(state DOUBLE PRECISION[])
RETURNS DOUBLE PRECISION LANGUAGE sql IMMUTABLE AS $$ SELECT state[1] $$;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='analytics' AND p.proname='arg_max') THEN
 CREATE AGGREGATE arg_max(DOUBLE PRECISION,BIGINT)
 (SFUNC=last_observed,STYPE=DOUBLE PRECISION[],FINALFUNC=observed_value);
 END IF;
END $$;
CREATE OR REPLACE FUNCTION isfinite(value DOUBLE PRECISION) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE AS $$ SELECT value>'-Infinity'::DOUBLE PRECISION AND value<'Infinity'::DOUBLE PRECISION $$;
CREATE OR REPLACE FUNCTION contains(value TEXT,needle TEXT) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE AS $$ SELECT strpos(value,needle)>0 $$;
