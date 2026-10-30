import { execFileSync, spawnSync } from "node:child_process";
import { pathToFileURL } from "node:url";

// Shared readers can use the same SQL outside management. A shape alone does
// not identify an endpoint; retain the PID+transaction request correlation.
const kinds = new Set(["receipt_read", "page_models", "model_directory", "rates", "model_signals", "receipt_signals"]);
const identity = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/;
const kubernetesName = /^[a-z0-9][a-z0-9.-]{0,252}$/;
const requestID = /^[a-zA-Z0-9._:-]{1,128}$/;
const blockerKinds = new Set([...kinds, "global_advisory_lock", "exporter_state_lock", "other"]);

export function optionsFrom(args) {
  const allowed = new Set(["ssh-host", "namespace", "pod", "logs-pod", "database", "user", "app", "duration", "interval-ms"]);
  const values = { database: "console", user: "postgres", app: "uni-api-analytics", duration: "35", "interval-ms": "250" };
  const seen = new Set();
  for (let i = 0; i < args.length; i += 2) {
    const key = args[i]?.replace(/^--/, "");
    if (!args[i]?.startsWith("--") || !allowed.has(key) || seen.has(key) || !args[i + 1]) throw Error("Invalid observer arguments");
    seen.add(key);
    values[key] = args[i + 1];
  }
  for (const key of ["ssh-host", "database", "user", "app"]) if (!identity.test(values[key] ?? "")) throw Error("Invalid observer target");
  for (const key of ["namespace", "pod"]) if (!kubernetesName.test(values[key] ?? "")) throw Error("Invalid observer target");
  if (values["logs-pod"] !== undefined && !kubernetesName.test(values["logs-pod"])) throw Error("Invalid log target");
  const duration = Number(values.duration), interval = Number(values["interval-ms"]);
  if (!Number.isInteger(duration) || duration < 1 || duration > 45 || !Number.isInteger(interval) || interval < 250 || interval > 1000) throw Error("Observation must be bounded to 1–45 seconds and 250–1000 ms intervals");
  return { ...values, duration, interval };
}

// All SQL is fixed except validated numeric bounds. No business query or
// parameter is replayed. Cumulative backend I/O may lag; it is NOT per-query I/O.
export function observationSQL(options) {
  const ticks = Math.floor(options.duration * 1000 / options.interval);
  const timeout = options.duration + 3;
  return `BEGIN READ ONLY;
SET LOCAL statement_timeout='${timeout}s';
DO $observe$
DECLARE sample record; io jsonb; has_io boolean;
BEGIN
 has_io := pg_catalog.to_regprocedure('pg_catalog.pg_stat_get_backend_io(integer)') IS NOT NULL;
 FOR i IN 1..${ticks} LOOP
  PERFORM pg_catalog.pg_stat_clear_snapshot();
  FOR sample IN
   WITH activity AS MATERIALIZED (
   SELECT floor(extract(epoch FROM clock_timestamp())*1000)::bigint AS at_ms,
    pid,floor(extract(epoch FROM xact_start)*1000)::bigint AS xact_started_ms,
    floor(extract(epoch FROM query_start)*1000)::bigint AS query_started_ms,
    state,wait_event_type AS wait_type,wait_event AS wait,
    query LIKE 'WITH first AS (SELECT key_id,started_at/86400 AS day,scan_page,scan_fingerprint FROM console_sub_usage%' AS usage_batch,
    CASE WHEN query LIKE 'SELECT u.id,u.status,u.result FROM console_sub_usage%' THEN 'receipt_read'
     WHEN query LIKE 'WITH wanted AS MATERIALIZED%' THEN 'page_models'
     WHEN query LIKE 'SELECT m.account_id,m.group_id,m.model,m.state,m.message%' THEN 'model_directory'
     WHEN query LIKE 'WITH owned_targets AS MATERIALIZED%' THEN 'rates'
     WHEN query LIKE '%console_management_model_signal%' THEN 'model_signals'
     WHEN query LIKE '%console_management_receipt_signal%' THEN 'receipt_signals' END AS kind
   FROM pg_catalog.pg_stat_activity
   WHERE datname=current_database() AND application_name='uni-api-control'
    AND backend_type='client backend' AND pid<>pg_backend_pid() AND xact_start IS NOT NULL
    AND state IN ('active','idle in transaction','idle in transaction (aborted)')),
   concurrent AS MATERIALIZED (
    SELECT jsonb_build_object(
     'active_control_backends',count(*) FILTER(WHERE state='active'),
     'background_usage_batches',count(*) FILTER(WHERE state='active' AND usage_batch),
     'background_usage_batches_data_file_read',count(*) FILTER(WHERE state='active' AND usage_batch AND wait_type='IO' AND wait='DataFileRead'),
     'background_usage_batches_max_query_ms',COALESCE(max(greatest(0,at_ms-query_started_ms)) FILTER(WHERE state='active' AND usage_batch),0)
    ) AS concurrent_activity FROM activity),
   selected AS MATERIALIZED (
    SELECT * FROM activity CROSS JOIN concurrent WHERE kind IS NOT NULL
    ORDER BY pid LIMIT 16),
   blocked AS MATERIALIZED (
    SELECT *,pg_catalog.pg_blocking_pids(pid) AS blocking_pids FROM selected)
   SELECT at_ms,pid,xact_started_ms,query_started_ms,state,wait_type,wait,kind,concurrent_activity,
    cardinality(blocking_pids) AS reported_blockers,
    (SELECT COALESCE(jsonb_agg(jsonb_build_object(
      'pid',b.pid,'xact_started_ms',floor(extract(epoch FROM b.xact_start)*1000)::bigint,
      'query_started_ms',floor(extract(epoch FROM b.query_start)*1000)::bigint,
      'application',CASE b.application_name WHEN 'uni-api-control' THEN 'control' WHEN 'uni-api-analytics' THEN 'analytics' ELSE 'other' END,
      'kind',CASE WHEN b.query LIKE 'SELECT u.id,u.status,u.result FROM console_sub_usage%' THEN 'receipt_read'
       WHEN b.query LIKE 'WITH wanted AS MATERIALIZED%' THEN 'page_models'
       WHEN b.query LIKE 'SELECT m.account_id,m.group_id,m.model,m.state,m.message%' THEN 'model_directory'
       WHEN b.query LIKE 'WITH owned_targets AS MATERIALIZED%' THEN 'rates'
       WHEN b.query LIKE '%console_management_model_signal%' THEN 'model_signals'
       WHEN b.query LIKE '%console_management_receipt_signal%' THEN 'receipt_signals'
       WHEN b.query LIKE 'SELECT pg_advisory_xact_lock(8395015)%' THEN 'global_advisory_lock'
       WHEN b.query LIKE 'SELECT pg_advisory_xact_lock(hashtextextended(%' THEN 'exporter_state_lock'
       ELSE 'other' END,
      'state',b.state,'wait_type',b.wait_event_type,'wait',b.wait_event)), '[]'::jsonb)
     FROM (SELECT a.pid,a.xact_start,a.query_start,a.application_name,a.state,a.wait_event_type,a.wait_event,a.query
      FROM pg_catalog.pg_stat_activity a WHERE a.pid=ANY(activity.blocking_pids)
      ORDER BY a.pid LIMIT 8) b) AS blockers
   FROM blocked activity ORDER BY pid
  LOOP
   IF sample.kind IS NOT NULL THEN
    io := NULL;
    IF has_io THEN
     EXECUTE 'SELECT COALESCE(jsonb_agg(jsonb_build_object(''object'',object,''context'',context,
      ''reads'',reads,''read_bytes'',read_bytes,''hits'',hits)),''[]''::jsonb)
      FROM pg_catalog.pg_stat_get_backend_io($1)' INTO io USING sample.pid;
    END IF;
    RAISE NOTICE '%',row_to_json(sample)::jsonb || jsonb_build_object('backend_io',io);
   END IF;
  END LOOP;
  PERFORM pg_catalog.pg_sleep(${options.interval / 1000});
 END LOOP;
END $observe$;
ROLLBACK;`;
}

const integer = n => Number.isSafeInteger(n) && n >= 0;
const label = v => v === null || typeof v === "string" && /^[a-zA-Z0-9_ ():-]{1,64}$/.test(v);

// Only PostgreSQL's actual blocking PIDs establish a lock dependency. Other
// concurrent queries and I/O waits remain context, never inferred blockers.
export function blockingActivity(value) {
  if (!Array.isArray(value) || value.length > 8) return null;
  const states = new Set(["active", "idle", "idle in transaction", "idle in transaction (aborted)", "fastpath function call", "disabled"]);
  const seen = new Set();
  const out = [];
  for (const row of value) {
    if (!row || !integer(row.pid) || row.pid === 0 || seen.has(row.pid) ||
      ![row.xact_started_ms, row.query_started_ms].every(v => v === null || integer(v)) ||
      !["control", "analytics", "other"].includes(row.application) || !blockerKinds.has(row.kind) || !states.has(row.state) || !label(row.wait_type) || !label(row.wait)) return null;
    seen.add(row.pid);
    out.push({ pid: row.pid, xact_started_ms: row.xact_started_ms, query_started_ms: row.query_started_ms,
      application: row.application, kind: row.kind, state: row.state, wait_type: row.wait_type, wait: row.wait });
  }
  return out;
}

// Counts describe one shared activity snapshot, not per-request I/O or a cause
// of its delay. Missing/invalid context stays unavailable, never a false zero.
export function concurrentActivity(value) {
  const keys = ["active_control_backends", "background_usage_batches", "background_usage_batches_data_file_read", "background_usage_batches_max_query_ms"];
  if (!value || typeof value !== "object" || Array.isArray(value) || !keys.every(key => integer(value[key]))) return null;
  if (value.background_usage_batches_data_file_read > value.background_usage_batches || value.background_usage_batches > value.active_control_backends ||
    value.background_usage_batches === 0 && value.background_usage_batches_max_query_ms !== 0) return null;
  return Object.fromEntries(keys.map(key => [key, value[key]]));
}

export function safeSamples(stderr) {
  const samples = [];
  for (const line of stderr.split("\n")) {
    if (!/^NOTICE:\s+\{/.test(line)) continue;
    let value;
    try { value = JSON.parse(line.slice(line.indexOf("{"))); } catch { continue; }
    if (!value || typeof value !== "object" || !integer(value.at_ms) || !integer(value.pid) || value.pid === 0 || !integer(value.xact_started_ms) || !integer(value.query_started_ms) || !kinds.has(value.kind)) continue;
    if (!["active", "idle in transaction", "idle in transaction (aborted)"].includes(value.state) || !label(value.wait_type) || !label(value.wait)) continue;
    const io = value.backend_io === null ? null : Array.isArray(value.backend_io) ? value.backend_io.flatMap(v => {
      if (!label(v?.object) || !label(v?.context) || ![v?.reads, v?.read_bytes, v?.hits].every(n => n === null || integer(n))) return [];
      return [{ object: v.object, context: v.context, reads: v.reads, read_bytes: v.read_bytes, hits: v.hits }];
    }) : null;
    samples.push({ at_ms: value.at_ms, pid: value.pid, xact_started_ms: value.xact_started_ms, query_started_ms: value.query_started_ms,
      state: value.state, wait_type: value.wait_type, wait: value.wait, kind: value.kind, backend_io: io, io_scope: "backend_cumulative_may_lag",
      concurrent_activity: concurrentActivity(value.concurrent_activity), activity_scope: "database_concurrent_not_causal", kind_scope: "query_shape_not_request_type",
      blockers: blockingActivity(value.blockers), reported_blockers: integer(value.reported_blockers) ? value.reported_blockers : null,
      blocker_scope: "pg_blocking_pids_up_to_8_current_snapshot" });
  }
  return samples;
}

export function sessionRequests(logs) {
  const sessions = new Map();
  for (const log of logs ?? []) {
    const match = typeof log?.message === "string" && log.message.match(/(?:^| )management_database_session phase=(?:proof|view) backend_pid=(\d+) request_id=([a-zA-Z0-9._:-]{1,128}) xact_started_ms=(\d+)$/);
    if (!match || !requestID.test(match[2]) || !integer(Number(match[1])) || !integer(Number(match[3]))) continue;
    const key = `${Number(match[1])}:${Number(match[3])}`;
    if (!sessions.has(key)) sessions.set(key, new Set());
    sessions.get(key).add(match[2]);
  }
  return sessions;
}

export function correlate(samples, sessions) {
  return samples.map(sample => {
    const ids = [...(sessions.get(`${sample.pid}:${sample.xact_started_ms}`) ?? [])].sort();
    const blockers = sample.blockers?.map(blocker => {
      const request_ids = [...(sessions.get(`${blocker.pid}:${blocker.xact_started_ms}`) ?? [])].sort();
      return { ...blocker, request_ids, correlation: request_ids.length === 1 ? "pid_xact_ms" : request_ids.length ? "ambiguous" : "unmatched" };
    }) ?? null;
    return { ...sample, blockers, request_ids: ids, correlation: ids.length === 1 ? "pid_xact_ms" : ids.length ? "ambiguous" : "unmatched" };
  });
}

export function runObserver(options, { execute = execFileSync, spawn = spawnSync } = {}) {
  const remote = `kubectl -n ${options.namespace} exec -i ${options.pod} -- psql -X -U ${options.user} -d ${options.database} -Atq -v ON_ERROR_STOP=1`;
  let completed = true, stderr = "";
  try {
    // psql NOTICE goes to stderr, not stdout. Never forward raw subprocess
    // output/errors: SQL and credentials must not escape an unexpected failure.
    const result = spawn("ssh", ["-o", "BatchMode=yes", "-o", "ConnectTimeout=6", options["ssh-host"], remote],
      { input: observationSQL(options), encoding: "utf8", timeout: (options.duration + 10) * 1000, maxBuffer: 4 << 20, stdio: ["pipe", "pipe", "pipe"] });
    completed = result.status === 0;
    stderr = String(result.stderr ?? "");
  } catch (error) {
    completed = false;
    stderr = String(error.stderr ?? "");
  }
  let available = false, logs = [], logSource = "unavailable";
  try {
    const result = JSON.parse(execute("fugue", ["app", "logs", "query", options.app, "--since", "5m", "--grep", "management_database_session", "--limit", "500", "--json"],
      { encoding: "utf8", timeout: 10000, maxBuffer: 4 << 20, stdio: ["pipe", "pipe", "pipe"] }));
    available = result.source?.status === "available";
    if (available && Array.isArray(result.logs)) logs = result.logs;
    if (available) logSource = "fugue";
  } catch { /* Observation failure never changes or retries a business request. */ }
  // Opt-in fallback for an unavailable control-plane API. Read only the exact
  // declared pod in the same namespace/host, never discover a broader target.
  // Raw logs stay private; sessionRequests admits only the fixed event schema.
  if (!available && options["logs-pod"]) {
    try {
      const remoteLogs = `kubectl -n ${options.namespace} logs ${options["logs-pod"]} --all-containers=true --since=5m --tail=2000`;
      const raw = execute("ssh", ["-o", "BatchMode=yes", "-o", "ConnectTimeout=6", options["ssh-host"], remoteLogs],
        { encoding: "utf8", timeout: 10000, maxBuffer: 4 << 20, stdio: ["pipe", "pipe", "pipe"] });
      logs = String(raw).split("\n").map(message => ({ message }));
      available = true;
      logSource = "ssh";
    } catch { /* Keep incomplete evidence explicit, without forwarding errors. */ }
  }
  return { completed, logs_available: available, logs_source: logSource, samples: correlate(safeSamples(stderr), sessionRequests(logs)) };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const result = runObserver(optionsFrom(process.argv.slice(2)));
    for (const sample of result.samples) console.log(JSON.stringify(sample));
    console.log(JSON.stringify({ completed: result.completed, logs_available: result.logs_available, logs_source: result.logs_source, samples: result.samples.length,
      correlated: result.samples.filter(s => s.correlation === "pid_xact_ms").length }));
    if (!result.completed) process.exitCode = 1;
  } catch {
    console.error("Observer failed; check explicit targets and bounded arguments. No business operation was performed.");
    process.exitCode = 1;
  }
}
