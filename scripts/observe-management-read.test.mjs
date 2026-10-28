import assert from "node:assert/strict";
import test from "node:test";
import { spawn, spawnSync, execFileSync } from "node:child_process";
import { observationSQL, optionsFrom, safeSamples, sessionRequests, correlate, runObserver } from "./observe-management-read.mjs";

const args = ["--ssh-host", "control", "--namespace", "tenant", "--pod", "database-1"];
const options = optionsFrom(args);
const sample = { at_ms: 1793224800999, pid: 42, xact_started_ms: 1793224800123, query_started_ms: 1793224800456,
  state: "active", wait_type: "IO", wait: "DataFileRead", kind: "receipt_read", backend_io: [{ object: "relation", context: "normal", reads: 8, read_bytes: 65536, hits: 9 }] };
const notice = value => `NOTICE:  ${JSON.stringify(value)}\n`;
const log = (id, xact = sample.xact_started_ms, phase = "view") => ({ message: `2026/10/29 06:13:14 management_database_session phase=${phase} backend_pid=42 request_id=${id} xact_started_ms=${xact}` });

test("requires explicit safe targets and bounded cadence, without shell interpolation", () => {
  assert.equal(options.duration, 35);
  assert.equal(options.interval, 250);
  for (const bad of [[], [...args, "--duration", "46"], [...args, "--interval-ms", "1"], [...args, "--duration", "1.5"], [...args, "--duration", "NaN"], [...args, "--unknown", "x"], [...args, "--app"], [...args, "--app", "x", "--app", "y"]]) assert.throws(() => optionsFrom(bad));
  for (const [key, bad] of [["ssh-host", "-oProxyCommand=secret"], ["namespace", "x;echo credential"], ["pod", "$(secret)"], ["user", "user password=x"], ["database", "postgres://secret"]]) {
    const index = args.indexOf(`--${key}`);
    const input = [...args];
    if (index < 0) input.push(`--${key}`, bad); else input[index + 1] = bad;
    assert.throws(() => optionsFrom(input));
  }
});

test("SQL only observes activity, with read-only transaction and bounded timeout", () => {
  const sql = observationSQL(options);
  assert.match(sql, /^BEGIN READ ONLY;/);
  assert.match(sql, /statement_timeout='38s'/);
  assert.match(sql, /FOR i IN 1\.\.140 LOOP/);
  assert.match(sql, /pg_sleep\(0\.25\)/);
  assert.match(sql, /LIMIT 16/);
  assert.match(sql, /floor\(extract\(epoch FROM xact_start\)\*1000\)::bigint/);
  assert.match(sql, /to_regprocedure\('pg_catalog\.pg_stat_get_backend_io\(integer\)'\)/);
  assert.match(sql, /ROLLBACK;$/);
  assert.doesNotMatch(sql, /EXPLAIN|INSERT|UPDATE|DELETE|ALTER|VACUUM|track_io_timing|plan_cache_mode|pg_stat_force_next_flush/i);
});

test("only whitelisted sample fields leave the observer", () => {
  const raw = notice({ ...sample, owner: "private-owner", query: "SELECT private-secret", credential: "secret", backend_io: [{ ...sample.backend_io[0], private: "secret" }] });
  const parsed = safeSamples(`Defaulted container private-secret\n${raw}ERROR: private SQL\n`);
  assert.equal(parsed.length, 1);
  assert.equal(parsed[0].io_scope, "backend_cumulative_may_lag");
  assert.equal(parsed[0].backend_io[0].reads, 8);
  assert.doesNotMatch(JSON.stringify(parsed), /private|secret|credential|SELECT/);
  for (const value of [{ ...sample, pid: 0 }, { ...sample, kind: "private SQL" }, { ...sample, wait: "private/secret" }, { ...sample, xact_started_ms: null }, { ...sample, at_ms: Number.MAX_SAFE_INTEGER + 1 }]) assert.deepEqual(safeSamples(notice(value)), []);
  assert.deepEqual(safeSamples(`CONTEXT: ${JSON.stringify(sample)}\n`), []);
  assert.deepEqual(safeSamples("NOTICE:  {malformed}\nNOTICE:  null\n"), []);
  assert.equal(safeSamples(notice({ ...sample, backend_io: null }))[0].backend_io, null);
});

test("same PID reused by consecutive requests cannot cross-correlate", () => {
  const sessions = sessionRequests([log("previous", sample.xact_started_ms - 100), log("current", undefined, "proof"), log("current")]);
  const rows = correlate(safeSamples(notice(sample) + notice({ ...sample, xact_started_ms: sample.xact_started_ms + 100 })), sessions);
  assert.deepEqual(rows[0].request_ids, ["current"]);
  assert.equal(rows[0].correlation, "pid_xact_ms");
  assert.deepEqual(rows[1].request_ids, []);
  assert.equal(rows[1].correlation, "unmatched");
  assert.deepEqual(correlate([sample], sessionRequests([log("a"), log("b")]))[0].request_ids, ["a", "b"]);
  assert.equal(correlate([sample], sessionRequests([log("a"), log("b")]))[0].correlation, "ambiguous");
});

test("legacy PID-only logs and malformed messages remain unmatched", () => {
  const legacy = { message: "management_database_session phase=view backend_pid=42 request_id=legacy" };
  const invalid = { message: log("private/credential").message };
  assert.equal(sessionRequests([legacy, invalid, { message: "unrelated private payload" }, {}, null]).size, 0);
});

test("runtime only runs one bounded observer and one log read; raw outputs never escape", () => {
  const calls = [];
  const result = runObserver(options, {
    spawn(command, argv, config) {
      calls.push(command);
      assert.equal(command, "ssh");
      assert.equal(argv[4], "control");
      assert.equal(argv[5], "kubectl -n tenant exec -i database-1 -- psql -X -U postgres -d console -Atq -v ON_ERROR_STOP=1");
      assert.equal(config.timeout, 45000);
      assert.match(config.input, /^BEGIN READ ONLY;/);
      return { status: 0, stdout: "private secret", stderr: notice({ ...sample, query: "private secret" }) };
    },
    execute(command, argv) {
      calls.push(command);
      assert.equal(command, "fugue");
      assert.deepEqual(argv.slice(0, 4), ["app", "logs", "query", "uni-api-analytics"]);
      return JSON.stringify({ source: { status: "available" }, logs: [log("current"), { message: "private secret" }] });
    },
  });
  assert.deepEqual(calls, ["ssh", "fugue"]);
  assert.equal(result.completed, true);
  assert.equal(result.logs_available, true);
  assert.equal(result.samples[0].correlation, "pid_xact_ms");
  assert.doesNotMatch(JSON.stringify(result), /private|secret/);
});

test("partial observation and missing logs remain explicitly incomplete", () => {
  const result = runObserver(options, {
    spawn() { return { status: null, stderr: notice(sample) + "ERROR: private secret" }; },
    execute() { throw Error("private credential"); },
  });
  assert.equal(result.completed, false);
  assert.equal(result.logs_available, false);
  assert.equal(result.samples[0].correlation, "unmatched");
  assert.doesNotMatch(JSON.stringify(result), /private|secret|credential/);
});

// Optional integration fixture. It is deliberately restricted to a local
// database, never an online DSN. Both synthetic activity and the observer use
// this isolated database; temporary fixture rows disappear on rollback.
test("PostgreSQL observation correlates the exact synthetic local transaction", { skip: !process.env.TEST_MANAGEMENT_OBSERVER_DATABASE_URL }, async () => {
  const dsn = process.env.TEST_MANAGEMENT_OBSERVER_DATABASE_URL;
  const url = new URL(dsn);
  assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(url.hostname), "integration fixture requires local PostgreSQL");
  assert.ok([...url.searchParams.keys()].every(key => key === "sslmode"), "integration fixture refuses connection redirects");
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("PG")));
  Object.assign(env, { PGHOST: url.hostname.replace(/^\[|\]$/g, ""), PGPORT: url.port || "5432", PGDATABASE: decodeURIComponent(url.pathname.slice(1)), PGSSLMODE: "disable" });
  if (url.username) env.PGUSER = decodeURIComponent(url.username);
  if (url.password) env.PGPASSWORD = decodeURIComponent(url.password);
  const activity = spawn("psql", ["-X", "-Atq", "-v", "ON_ERROR_STOP=1"], { env, stdio: ["pipe", "pipe", "pipe"] });
  let output = "", resolveReady, rejectReady;
  const ready = new Promise((resolve, reject) => { resolveReady = resolve; rejectReady = reject; });
  const timer = setTimeout(() => rejectReady(Error("local fixture startup timed out")), 4000);
  activity.stdout.on("data", chunk => {
    output += chunk;
    if (output.includes("\n")) resolveReady(output.split("\n")[0]);
  });
  activity.once("error", () => rejectReady(Error("local fixture unavailable")));
  activity.once("close", () => { if (!output.includes("\n")) rejectReady(Error("local fixture did not start")); });
  activity.stdin.end(`BEGIN;
CREATE TEMP TABLE console_sub_usage(id text,status text,result jsonb);
INSERT INTO console_sub_usage VALUES('private-fixture','matched','{"private":"not-output"}');
SET LOCAL application_name='uni-api-control';
SELECT pg_backend_pid()::text||':'||floor(extract(epoch FROM transaction_timestamp())*1000)::bigint::text;
SELECT u.id,u.status,u.result FROM console_sub_usage u CROSS JOIN LATERAL pg_sleep(4);
ROLLBACK;`);
  try {
    const key = await ready;
    const local = optionsFrom([...args, "--duration", "1"]);
    const observed = spawnSync("psql", ["-X", "-Atq", "-v", "ON_ERROR_STOP=1"], { env, input: observationSQL(local), encoding: "utf8", timeout: 6000 });
    assert.equal(observed.status, 0, "read-only local observer failed");
    const samples = safeSamples(observed.stderr);
    assert.ok(samples.length > 0, "no synthetic local activity observed");
    for (const value of samples) {
      assert.equal(`${value.pid}:${value.xact_started_ms}`, key);
      assert.equal(value.kind, "receipt_read");
      assert.equal(value.state, "active");
      assert.equal(value.wait, "PgSleep");
    }
    const version = Number(execFileSync("psql", ["-X", "-Atqc", "SHOW server_version_num"], { env, encoding: "utf8" }).trim());
    if (version < 180000) assert.ok(samples.every(s => s.backend_io === null));
    assert.doesNotMatch(JSON.stringify(samples), /private-fixture|not-output/);
  } finally {
    clearTimeout(timer);
    activity.kill("SIGTERM");
  }
});
