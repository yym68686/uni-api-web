import { expect, it } from "vitest";
import {
  buildTrace,
  eventHTTP,
  eventResult,
  traceChannel,
} from "./requestTraceModel";
import type { TraceEvent, TraceRun } from "./requestTraceModel";
const event = (patch: Partial<TraceEvent>): TraceEvent => ({
  event_id: "",
  kind: "",
  stage: "",
  at_ms: 1000,
  started_ms: null,
  attempt_id: "",
  provider: "",
  model: "",
  upstream_model: "",
  endpoint: "",
  stream: false,
  outcome: "",
  status: 0,
  terminal_kind: "",
  failure_reason: "",
  response_completed: null,
  duration_ms: null,
  dispatch_ms: null,
  response_created_ms: null,
  first_text_ms: null,
  detail: {},
  transport: {},
  ...patch,
});
const run = (events: TraceEvent[]): TraceRun => ({
  source_id: "s",
  instance_id: "i",
  request_id: "r",
  ambiguous: false,
  events,
});

it("separates 24 preparation skips from two sends and trusts observed downstream HTTP", () => {
  const events = Array.from({ length: 26 }, (_, i) => {
    const n = i + 1;
    return event({
      event_id: `candidate-${n}`,
      kind: n === 16 || n === 19 ? "dispatch" : "trace",
      stage: n === 16 || n === 19 ? "" : "routing_attempt",
      attempt_id: `r-r${n}`,
      provider: `p${n}`,
      at_ms: 1000 + n,
      detail:
        n === 16 || n === 19
          ? {}
          : {
              attempt_index: n,
              attempt_outcome: "skipped",
              skip_reason: "attempt_build_failed",
              phase: "request_conversion",
              error: {
                error_code: "gemini_tool_history",
                error_param: "messages[45].tool_calls[0]",
                error_message: "invalid tool identity",
              },
            },
    });
  });
  events.push(
    event({
      event_id: "final",
      kind: "request",
      status: 400,
      outcome: "failed",
      at_ms: 1100,
    }),
    event({
      event_id: "wire",
      stage: "response_headers",
      status: 401,
      at_ms: 1101,
    }),
    event({
      event_id: "facts",
      stage: "request_final_facts",
      provider: "p19",
      at_ms: 1102,
      detail: {
        final_facts: {
          downstream_http_status: 401,
          semantic_status: 401,
          terminal: "rejected",
        },
      },
    }),
  );
  const trace = buildTrace(run(events.reverse()));
  expect(trace.attempts.map((a) => a.candidateIndex)).toEqual([16, 19]);
  expect(trace.skipped).toHaveLength(24);
  expect(trace.unresolved).toHaveLength(0);
  expect(trace.skipped[0]).toMatchObject({
    sent: false,
    skipped: true,
    duration: null,
    status: 0,
  });
  expect(trace.skipped[0].errors.join()).toContain(
    "messages[45].tool_calls[0]",
  );
  expect(trace.gateway.some((e) => e.event_id === "facts")).toBe(true);
  expect(trace.downstreamHTTP).toBe(401);
  expect(trace.statusConflict).toBe(true);
  expect(trace.httpSource).toBe("observed");
});

it("keeps HTTP 200 separate from semantic failure and supports final-facts-only traces", () => {
  const trace = buildTrace(
    run([
      event({ kind: "request", status: 503, outcome: "failed", at_ms: 1100 }),
      event({
        stage: "request_final_facts",
        provider: "p",
        at_ms: 1101,
        detail: {
          final_facts: {
            terminal: "semantic_failure",
            response_completed: false,
            downstream_http_status: 200,
            semantic_status: 503,
          },
        },
      }),
    ]),
  );
  expect(trace.downstreamHTTP).toBe(200);
  expect(eventResult(trace.final)).toEqual({ label: "失败", tone: "failure" });
  expect(trace.attempts).toHaveLength(0);
  expect(eventHTTP(event({ detail: { semantic_status_code: 503 } }))).toBe(0);
  expect(
    eventResult(
      event({ status: 400, detail: { skip_reason: "attempt_build_failed" } }),
    ).label,
  ).toBe("已跳过");
});

it("does not count selection-only events as sends or join ambiguous downstream statuses", () => {
  const events = [
    event({
      event_id: "selection",
      stage: "routing_attempt",
      provider: "p",
      attempt_id: "r-r1",
      detail: { attempt_outcome: "started" },
    }),
    event({ event_id: "wire", stage: "response_headers", status: 401 }),
  ];
  const trace = buildTrace(run(events));
  expect(trace.attempts).toHaveLength(0);
  expect(trace.unresolved).toHaveLength(1);
  expect(buildTrace({ ...run(events), ambiguous: true }).downstreamHTTP).toBe(
    0,
  );
});
it("keeps concurrent attempts separate and merges records only by exact attempt identity", () => {
  const result = buildTrace(
    run([
      event({ event_id: "start", stage: "request_received", at_ms: 1000 }),
      event({
        event_id: "d1",
        kind: "dispatch",
        attempt_id: "1",
        provider: "p",
        at_ms: 1010,
      }),
      event({
        event_id: "d2",
        kind: "dispatch",
        attempt_id: "2",
        provider: "p",
        at_ms: 1020,
      }),
      event({
        event_id: "a2",
        kind: "attempt",
        attempt_id: "2",
        at_ms: 1050,
        outcome: "success",
        duration_ms: 30,
      }),
      event({
        event_id: "b2",
        kind: "billing",
        attempt_id: "2",
        provider: "p",
        at_ms: 1050,
        status: 200,
      }),
      event({
        event_id: "a1",
        kind: "attempt",
        attempt_id: "1",
        provider: "p",
        at_ms: 1060,
        outcome: "hedge_cancelled",
        duration_ms: 50,
      }),
      event({
        event_id: "end",
        kind: "request",
        attempt_id: "2",
        provider: "p",
        at_ms: 1060,
        status: 200,
        outcome: "success",
        duration_ms: 60,
      }),
    ]),
  );
  expect(result.attempts).toHaveLength(2);
  expect(result.attempts[0]).toMatchObject({
    started: 1010,
    end: 1060,
    duration: 50,
    tone: "warning",
  });
  expect(result.attempts[1]).toMatchObject({
    started: 1020,
    end: 1050,
    duration: 30,
    tone: "success",
  });
  expect(result.attempts[1].events).toHaveLength(3);
  expect(result.span).toBe(60);
  expect(result.final?.duration_ms).toBe(60);
});
it("does not invent an ingress or duration for an orphan receipt, or success from HTTP 200 alone", () => {
  expect(
    eventResult(
      event({
        status: 400,
        outcome: "failed",
        detail: {
          final_facts: { terminal: "completed", semantic_status: 200 },
        },
      }),
    ).tone,
  ).toBe("success");
  expect(
    eventResult(event({ status: 200, detail: { semantic_status_code: 503 } }))
      .tone,
  ).toBe("failure");
  const result = buildTrace(
    run([
      event({
        kind: "billing",
        event_id: "b",
        attempt_id: "1",
        provider: "p",
        status: 200,
      }),
    ]),
  );
  expect(result.arrival).toBeUndefined();
  expect(result.origin).toBe(1000);
  expect(result.attempts[0]).toMatchObject({ duration: null, tone: "neutral" });
  expect(
    eventResult(event({ stage: "response_headers", status: 200 })).tone,
  ).toBe("neutral");
  expect(
    eventResult(
      event({ kind: "request", status: 200, outcome: "failed/stream_error" }),
    ).tone,
  ).toBe("failure");
  expect(
    eventResult(
      event({
        kind: "attempt",
        status: 200,
        outcome: "success",
        response_completed: false,
      }),
    ).tone,
  ).toBe("failure");
  expect(buildTrace(run([]))).toMatchObject({ origin: 0, end: 0, span: 1 });
});
it("does not coalesce ambiguous runs or events without attempt IDs", () => {
  const events = [
    event({
      event_id: "d",
      kind: "dispatch",
      provider: "p",
      attempt_id: "same",
    }),
    event({
      event_id: "e",
      kind: "attempt",
      provider: "p",
      attempt_id: "same",
    }),
  ];
  expect(buildTrace({ ...run(events), ambiguous: true }).attempts).toHaveLength(
    2,
  );
  expect(
    buildTrace(run(events.map((e) => ({ ...e, attempt_id: "" })))).attempts,
  ).toHaveLength(2);
});
it("uses current known rate including zero and never guesses a rate from an opaque ID", () => {
  const data = run([event({ provider: "sub2api-deadbeef" })]);
  expect(traceChannel(data, "sub2api-deadbeef").name).toBe("未关联渠道 1");
  data.channels = {
    "sub2api-deadbeef": {
      name: "raw",
      site_name: "站点",
      rate: 0,
      group_name: "分组",
    },
  };
  expect(traceChannel(data, "sub2api-deadbeef").name).toBe("站点 · 0×");
});
