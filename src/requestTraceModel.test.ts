import { expect, it } from "vitest";
import { buildTrace, eventResult, traceChannel } from "./requestTraceModel";
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
