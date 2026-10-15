package main

// Historical schema batches retained only for upgrade fixture tests.
// Production migrations live in migrations/ and are versioned and immutable.

const controlPersistenceSchema = `CREATE TABLE IF NOT EXISTS console_control_snapshots (
 source_id TEXT PRIMARY KEY REFERENCES console_sources(id) ON DELETE CASCADE, enabled BOOLEAN NOT NULL DEFAULT true,
 encrypted_snapshot TEXT NOT NULL DEFAULT '', target_hash TEXT NOT NULL DEFAULT '',
 instance_id TEXT NOT NULL DEFAULT '', revision TEXT NOT NULL DEFAULT '',
 restoring_instance TEXT NOT NULL DEFAULT '', restore_revision TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending', message TEXT NOT NULL DEFAULT '',
 saved_at TIMESTAMPTZ, restored_at TIMESTAMPTZ, checked_at TIMESTAMPTZ,
 rule_count INTEGER NOT NULL DEFAULT 0, channel_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS console_control_bootstraps (
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 target_hash TEXT NOT NULL, snapshot_id TEXT NOT NULL, encrypted_snapshot TEXT NOT NULL,
 instance_id TEXT NOT NULL, revision TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,target_hash,snapshot_id));
CREATE TABLE IF NOT EXISTS console_control_retired_instances (
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 target_hash TEXT NOT NULL, instance_id TEXT NOT NULL, superseded_by TEXT NOT NULL,
 retired_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(source_id,target_hash,instance_id));`

const subSchema = `
CREATE TABLE IF NOT EXISTS console_sub_accounts(
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, name TEXT NOT NULL, base TEXT NOT NULL, email TEXT NOT NULL,
 encrypted_auth TEXT NOT NULL, encrypted_login_password TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '',
 job_kind TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ,
 synced_at BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner,base,email));
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS balance JSONB;
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS sync_models JSONB;
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS provider_kind TEXT NOT NULL DEFAULT 'sub2api';
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS login_name TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS encrypted_login_password TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS console_site_groups(
 id BIGSERIAL PRIMARY KEY, account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 remote_key TEXT NOT NULL, UNIQUE(account_id,remote_key));
CREATE TABLE IF NOT EXISTS console_site_key_creates(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,name TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(account_id,name));
CREATE TABLE IF NOT EXISTS console_site_api_schedule(
 site TEXT PRIMARY KEY,next_log_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS console_site_receipt_ids(
 id BIGSERIAL PRIMARY KEY,account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL,event_type INT NOT NULL,key_id BIGINT NOT NULL,
 UNIQUE(account_id,request_id,event_type,key_id));
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS usage_lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_accounts ADD COLUMN IF NOT EXISTS usage_lease_until TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS console_sub_usage(
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 group_id BIGINT NOT NULL, key_id BIGINT NOT NULL, model TEXT NOT NULL, started_at BIGINT NOT NULL,
 request_ids JSONB NOT NULL, status TEXT NOT NULL DEFAULT 'pending', result JSONB,
 attempts INT NOT NULL DEFAULT 0, next_attempt TIMESTAMPTZ NOT NULL DEFAULT now());
ALTER TABLE console_sub_usage ADD COLUMN IF NOT EXISTS scan_page INT NOT NULL DEFAULT 1;
ALTER TABLE console_sub_usage ADD COLUMN IF NOT EXISTS scan_match JSONB;
ALTER TABLE console_sub_usage ADD COLUMN IF NOT EXISTS scan_fingerprint TEXT NOT NULL DEFAULT '';
-- Resume receipts abandoned only because the legacy retry count was exhausted.
UPDATE console_sub_usage SET status='pending',next_attempt=now() WHERE status='missing';
CREATE TABLE IF NOT EXISTS console_sub_spend_logs(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 key_id BIGINT NOT NULL, log_id BIGINT NOT NULL, at_ms BIGINT NOT NULL,
 actual_cost NUMERIC(20,10) NOT NULL, PRIMARY KEY(account_id,key_id,log_id));
ALTER TABLE console_sub_spend_logs ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_spend_logs ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_spend_logs ADD COLUMN IF NOT EXISTS stream BOOLEAN;
ALTER TABLE console_sub_spend_logs ADD COLUMN IF NOT EXISTS inbound_endpoint TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS console_sub_spend_request ON console_sub_spend_logs(account_id,key_id,request_id);
CREATE INDEX IF NOT EXISTS console_sub_spend_time ON console_sub_spend_logs(account_id,key_id,at_ms);
-- An audited supplement for old immutable facts recovered from retained gateway
-- logs. No inferred amounts or raw error bodies are stored here.
CREATE TABLE IF NOT EXISTS console_billing_error_evidence(
 source_id TEXT NOT NULL,event_id TEXT NOT NULL,request_id TEXT NOT NULL,attempt_id TEXT NOT NULL,
 provider TEXT NOT NULL,status INT NOT NULL,error_sha256 TEXT NOT NULL CHECK(error_sha256 ~ '^[0-9a-f]{64}$'),
 log_sha256 TEXT NOT NULL CHECK(log_sha256 ~ '^[0-9a-f]{64}$'),
 provenance TEXT NOT NULL,recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(source_id,event_id));
CREATE TABLE IF NOT EXISTS console_sub_spend_cache(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 key_id BIGINT NOT NULL, group_id BIGINT NOT NULL,
 wanted_from BIGINT NOT NULL,wanted_to BIGINT NOT NULL,
 covered_from BIGINT,covered_to BIGINT,
 scan_from BIGINT NOT NULL DEFAULT 0,scan_to BIGINT NOT NULL DEFAULT 0,page INT NOT NULL DEFAULT 1,
 requested BOOLEAN NOT NULL DEFAULT true,checked_at BIGINT NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',attempts INT NOT NULL DEFAULT 0,next_attempt TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key_id));
CREATE INDEX IF NOT EXISTS console_sub_spend_due ON console_sub_spend_cache(account_id,next_attempt) WHERE requested;
-- A new account cursor must scan all keys. Per-key coverage is not proof of
-- account coverage. Keep the old table intact for rolling upgrade/rollback.
CREATE TABLE IF NOT EXISTS console_sub_account_spend_cache(
 account_id TEXT PRIMARY KEY REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 wanted_from BIGINT NOT NULL,wanted_to BIGINT NOT NULL,covered_from BIGINT,covered_to BIGINT,
 scan_from BIGINT NOT NULL DEFAULT 0,scan_to BIGINT NOT NULL DEFAULT 0,page INT NOT NULL DEFAULT 1,
 requested BOOLEAN NOT NULL DEFAULT true,checked_at BIGINT NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',attempts INT NOT NULL DEFAULT 0,next_attempt TIMESTAMPTZ NOT NULL DEFAULT now());
ALTER TABLE console_sub_account_spend_cache ADD COLUMN IF NOT EXISTS page_size INT NOT NULL DEFAULT 1000;
CREATE INDEX IF NOT EXISTS console_sub_account_spend_due ON console_sub_account_spend_cache(next_attempt) WHERE requested;
INSERT INTO console_sub_account_spend_cache(account_id,wanted_from,wanted_to)
 SELECT account_id,min(wanted_from),max(wanted_to) FROM console_sub_spend_cache GROUP BY account_id
 ON CONFLICT(account_id) DO NOTHING;
CREATE INDEX IF NOT EXISTS console_sub_usage_pending ON console_sub_usage(account_id,next_attempt,key_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS console_sub_accounts_owner ON console_sub_accounts(owner);
CREATE TABLE IF NOT EXISTS console_sub_auth_locks(
 account_id TEXT PRIMARY KEY REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 token TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS console_sub_targets(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE, group_id BIGINT NOT NULL,
 name TEXT NOT NULL, platform TEXT NOT NULL, channel TEXT NOT NULL DEFAULT '', rate DOUBLE PRECISION NOT NULL DEFAULT 1,
 remote_key_id BIGINT NOT NULL DEFAULT 0, encrypted_key TEXT NOT NULL DEFAULT '', active BOOLEAN NOT NULL DEFAULT true,
 state TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '', result JSONB,
 PRIMARY KEY(account_id,group_id));
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS billing JSONB;
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS compaction JSONB;
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS compaction_state TEXT NOT NULL DEFAULT 'idle';
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS tool_use JSONB;
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS tool_use_state TEXT NOT NULL DEFAULT 'idle';
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS encrypted_routing_key TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS routing_key_id BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS console_sub_models(
 account_id TEXT NOT NULL,group_id BIGINT NOT NULL,model TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'idle',message TEXT NOT NULL DEFAULT '',result JSONB,
 PRIMARY KEY(account_id,group_id,model),FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS console_sub_check_queue(
 account_id TEXT NOT NULL,group_id BIGINT NOT NULL,kind TEXT NOT NULL,model TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(account_id,group_id,kind,model),
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE);
INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT account_id,group_id,'gpt-6-astra','done',result FROM console_sub_targets WHERE result IS NOT NULL ON CONFLICT DO NOTHING;`

const subBindingSchema = `
CREATE TABLE IF NOT EXISTS console_configured_channels(
 source_id TEXT NOT NULL, provider TEXT NOT NULL, base TEXT NOT NULL, site TEXT NOT NULL,
 key_hashes JSONB NOT NULL, fingerprint TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT true,
 checked_at BIGINT NOT NULL, PRIMARY KEY(source_id,provider));
CREATE TABLE IF NOT EXISTS console_sub_key_scans(
 account_id TEXT PRIMARY KEY REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 revision TEXT NOT NULL DEFAULT '', lease_token TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ,
 checked_at BIGINT NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', next_attempt TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS console_sub_key_index(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 key_hash TEXT NOT NULL, remote_key_id BIGINT NOT NULL, group_id BIGINT NOT NULL,
 key_created_at TIMESTAMPTZ, PRIMARY KEY(account_id,key_hash));
ALTER TABLE console_sub_key_scans ADD COLUMN IF NOT EXISTS scan_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE console_sub_key_scans ADD COLUMN IF NOT EXISTS next_page INT NOT NULL DEFAULT 1;
ALTER TABLE console_sub_key_scans ADD COLUMN IF NOT EXISTS page_fingerprint TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS console_sub_key_index_stage(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 key_hash TEXT NOT NULL, remote_key_id BIGINT NOT NULL, group_id BIGINT NOT NULL,
 key_created_at TIMESTAMPTZ, PRIMARY KEY(account_id,key_hash));
CREATE TABLE IF NOT EXISTS console_channel_management_snapshots(
 source_id TEXT PRIMARY KEY REFERENCES console_sources(id) ON DELETE CASCADE,
 catalog JSONB NOT NULL, checked_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS console_channel_import_key_snapshots(
 source_id TEXT PRIMARY KEY REFERENCES console_sources(id) ON DELETE CASCADE,
 identity TEXT NOT NULL, directory JSONB NOT NULL, checked_at BIGINT NOT NULL);`

const qualityHistorySchema = `
CREATE TABLE IF NOT EXISTS console_quality_history(
 id BIGSERIAL PRIMARY KEY, check_id TEXT NOT NULL UNIQUE,
 source_id TEXT REFERENCES console_sources(id) ON DELETE CASCADE, provider TEXT,
 account_id TEXT, group_id BIGINT,
 model TEXT NOT NULL DEFAULT 'gpt-6-astra', rule TEXT NOT NULL DEFAULT 'candy-21',
 checked_at BIGINT NOT NULL, deadline TIMESTAMPTZ NOT NULL DEFAULT now(),
 verdict TEXT NOT NULL DEFAULT 'running', successful BOOLEAN NOT NULL DEFAULT false,
 result JSONB NOT NULL DEFAULT '{}',
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE,
 CHECK((source_id IS NOT NULL AND provider IS NOT NULL AND account_id IS NULL AND group_id IS NULL)
    OR (source_id IS NULL AND provider IS NULL AND account_id IS NOT NULL AND group_id IS NOT NULL)));
CREATE INDEX IF NOT EXISTS console_quality_channel_history ON console_quality_history(source_id,provider,id DESC);
CREATE INDEX IF NOT EXISTS console_quality_sub_history ON console_quality_history(account_id,group_id,id DESC);
CREATE TABLE IF NOT EXISTS console_quality_migrations(name TEXT PRIMARY KEY);
WITH first_run AS (
 INSERT INTO console_quality_migrations VALUES('latest-to-history-v1') ON CONFLICT DO NOTHING RETURNING name
)
INSERT INTO console_quality_history(check_id,source_id,provider,account_id,group_id,checked_at,verdict,successful,result,rule)
 SELECT 'legacy-channel:'||md5(source_id||':'||provider),source_id,provider,NULL,NULL,
 COALESCE((result->>'checked_at')::BIGINT,0),COALESCE(result->>'verdict','error'),
 COALESCE(result->>'verdict' IN ('pass','fail','inconclusive'),false),result,'legacy'
 FROM console_channel_checks WHERE EXISTS(SELECT 1 FROM first_run)
 UNION ALL
 SELECT 'legacy-sub:'||md5(account_id||':'||group_id),NULL,NULL,account_id,group_id,
 COALESCE((result->>'checked_at')::BIGINT,0),COALESCE(result->>'verdict','error'),
 COALESCE(result->'quality'->>'status'='success',false),result,'legacy'
 FROM console_sub_models WHERE model='gpt-6-astra' AND result IS NOT NULL AND EXISTS(SELECT 1 FROM first_run)
 ON CONFLICT DO NOTHING;
CREATE OR REPLACE VIEW console_quality_totals AS
 SELECT source_id,provider,account_id,group_id,count(*) AS total,
 count(*) FILTER(WHERE successful) AS successful,
 count(*) FILTER(WHERE successful AND verdict='pass') AS passed
 FROM console_quality_history GROUP BY source_id,provider,account_id,group_id;
CREATE TABLE IF NOT EXISTS console_quality_group_links(
 check_id TEXT PRIMARY KEY REFERENCES console_quality_history(check_id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL,
 linked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS console_quality_group_links_scope ON console_quality_group_links(account_id,group_id);
CREATE OR REPLACE VIEW console_quality_group_history AS
 SELECT h.id,h.check_id,h.model,h.rule,h.checked_at,h.deadline,h.verdict,h.successful,h.result,h.source_id,h.provider,
 COALESCE(h.account_id,l.account_id) AS account_id,COALESCE(h.group_id,l.group_id) AS group_id
 FROM console_quality_history h LEFT JOIN console_quality_group_links l USING(check_id)
 WHERE h.account_id IS NOT NULL OR l.account_id IS NOT NULL;`

const channelSettingsSchema = `CREATE TABLE IF NOT EXISTS console_channel_settings_operations(
 id TEXT PRIMARY KEY,source_id TEXT NOT NULL REFERENCES console_sources(id),owner TEXT NOT NULL,
 request_hash TEXT NOT NULL,request_revision TEXT NOT NULL,status TEXT NOT NULL,
 encrypted_before TEXT NOT NULL,encrypted_intent TEXT NOT NULL,public_result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE INDEX IF NOT EXISTS console_channel_settings_audit ON console_channel_settings_operations(source_id,created_at DESC);
 CREATE TABLE IF NOT EXISTS console_channel_setting_templates(id TEXT PRIMARY KEY,owner TEXT NOT NULL,name TEXT NOT NULL,patch JSONB NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS console_channel_protocol_operations(
 id TEXT PRIMARY KEY,source_id TEXT NOT NULL REFERENCES console_sources(id),owner TEXT NOT NULL,
 request_hash TEXT NOT NULL,request_revision TEXT NOT NULL,target_hash TEXT NOT NULL,status TEXT NOT NULL,
 encrypted_before TEXT NOT NULL,encrypted_intent TEXT NOT NULL,public_result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());`

const configuredCheckSchema = `
CREATE TABLE IF NOT EXISTS console_configured_checks(
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,kind TEXT NOT NULL,model TEXT NOT NULL DEFAULT '',
 source_target TEXT NOT NULL,fingerprint TEXT NOT NULL DEFAULT '',quality_only BOOLEAN NOT NULL DEFAULT false,
 state TEXT NOT NULL DEFAULT 'queued',message TEXT NOT NULL DEFAULT '',result JSONB,
 run_id TEXT NOT NULL DEFAULT '',deadline TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,provider,kind,model));
CREATE INDEX IF NOT EXISTS console_configured_checks_pending ON console_configured_checks(updated_at) WHERE state='queued';`

const modelFingerprintSchema = `
CREATE TABLE IF NOT EXISTS console_model_fingerprints(
 scope TEXT NOT NULL, model TEXT NOT NULL,
 source_id TEXT REFERENCES console_sources(id) ON DELETE CASCADE, provider TEXT NOT NULL DEFAULT '',
 account_id TEXT REFERENCES console_sub_accounts(id) ON DELETE CASCADE, group_id BIGINT NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'queued', result JSONB, message TEXT NOT NULL DEFAULT '',
 revision TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', deadline TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(scope,model));
CREATE INDEX IF NOT EXISTS console_model_fingerprints_pending ON console_model_fingerprints(updated_at) WHERE state='queued';
CREATE TABLE IF NOT EXISTS console_fingerprint_worker(id BOOLEAN PRIMARY KEY CHECK(id),run_id TEXT NOT NULL DEFAULT '',deadline TIMESTAMPTZ);
INSERT INTO console_fingerprint_worker(id) VALUES(true) ON CONFLICT DO NOTHING;
ALTER TABLE console_model_fingerprints ADD COLUMN IF NOT EXISTS site_key TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS console_fingerprint_site_leases(
 run_id TEXT PRIMARY KEY, site TEXT NOT NULL UNIQUE, deadline TIMESTAMPTZ NOT NULL);`

const automationSchema = `
CREATE TABLE IF NOT EXISTS console_automation_tasks(
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, name TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT false, source_id TEXT NOT NULL,
 key_id TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 interval_seconds INTEGER NOT NULL, range_name TEXT NOT NULL,
 policy JSONB NOT NULL DEFAULT '{}', next_run TIMESTAMPTZ NOT NULL DEFAULT now(),
 last_run TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(source_id) REFERENCES console_sources(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS console_automation_due ON console_automation_tasks(enabled,next_run);
CREATE TABLE IF NOT EXISTS console_automation_audit(
 id BIGSERIAL PRIMARY KEY, task_id TEXT NOT NULL REFERENCES console_automation_tasks(id) ON DELETE RESTRICT,
 run_at TIMESTAMPTZ NOT NULL DEFAULT now(), status TEXT NOT NULL, action TEXT NOT NULL,
 source_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
 before_order JSONB NOT NULL DEFAULT '[]', after_order JSONB NOT NULL DEFAULT '[]',
 changes JSONB NOT NULL DEFAULT '[]', metrics JSONB NOT NULL DEFAULT '{}',
 reason TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS console_automation_audit_task ON console_automation_audit(task_id,id DESC);
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'order';
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_audit ADD COLUMN IF NOT EXISTS task_name TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_audit ADD COLUMN IF NOT EXISTS policy JSONB NOT NULL DEFAULT '{}';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='console_automation_audit_task_id_fkey' AND confdeltype='c') THEN
    ALTER TABLE console_automation_audit DROP CONSTRAINT console_automation_audit_task_id_fkey;
    ALTER TABLE console_automation_audit ADD CONSTRAINT console_automation_audit_task_id_fkey FOREIGN KEY(task_id) REFERENCES console_automation_tasks(id) ON DELETE RESTRICT;
  END IF;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;`
