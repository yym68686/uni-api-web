import { expect, it } from "vitest";
import { buildTrace } from "./requestTraceModel";
import type { TraceEvent, TraceRun } from "./requestTraceModel";
import { attemptTiming, downstreamTiming } from "./requestTraceTiming";

const event = (patch: Partial<TraceEvent>): TraceEvent => ({
  event_id: "e",
  kind: "trace",
  stage: "",
  at_ms: 1000,
  started_ms: null,
  attempt_id: "a",
  provider: "p",
  model: "model",
  upstream_model: "model",
  endpoint: "/v1/responses",
  stream: true,
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
export const timingRun: TraceRun = {
  source_id: "primary",
  instance_id: "instance",
  request_id: "timing-request",
  ambiguous: false,
  channels: {
    p: {
      name: "测试站点 · 0.15×",
      site_name: "测试站点",
      rate: 0.15,
      group_name: "稳定渠道",
    },
  },
  events: [
    event({
      event_id: "ingress",
      stage: "request_received",
      at_ms: 1000,
      attempt_id: "",
      provider: "",
    }),
    event({ event_id: "dispatch", kind: "dispatch", at_ms: 1100 }),
    event({
      event_id: "bill",
      kind: "billing",
      started_ms: 1100,
      at_ms: 6100,
      detail: { headers_at_ms: 1300 },
    }),
    event({
      event_id: "attempt",
      kind: "attempt",
      at_ms: 6100,
      outcome: "success",
      status: 200,
      duration_ms: 5000,
      response_created_ms: 300,
      first_output_ms: 600,
      first_text_ms: 1000,
      transport: {
        origin: "attempt_http_send",
        headers_received_ms: 200,
        first_upstream_chunk_ms: 250,
        public_stream_ready_ms: 270,
        first_wire_prepared_ms: 310,
        last_upstream_chunk_ms: 4900,
      },
    }),
    event({
      event_id: "request",
      kind: "request",
      attempt_id: "",
      provider: "",
      at_ms: 6101,
      outcome: "success",
      status: 200,
      first_text_ms: 1000,
    }),
    event({
      event_id: "sent",
      stage: "response_body_finished",
      attempt_id: "",
      provider: "",
      at_ms: 6150,
    }),
  ],
};

it("places observed milestones on the upstream clock and preserves distinct response/text/end times", () => {
  const trace = buildTrace(timingRun);
  const result = attemptTiming(trace.attempts[0]);
  expect(
    Object.fromEntries(result.milestones.map((m) => [m.id, m.at])),
  ).toEqual({
    start: 1100,
    headers: 1300,
    "first-chunk": 1350,
    "stream-ready": 1370,
    created: 1400,
    "first-wire": 1410,
    "first-output": 1700,
    "first-text": 2100,
    "last-chunk": 6000,
    end: 6100,
  });
  expect(result.segments).toEqual([
    { phase: "waiting", label: "等待上游响应", from: 1100, to: 1300 },
    {
      phase: "responding",
      label: "响应已开始，等待正文",
      from: 1300,
      to: 2100,
    },
    { phase: "text", label: "首个正文之后", from: 2100, to: 6100 },
  ]);
  expect(result.missing).toEqual([]);
  expect(downstreamTiming(trace.gateway)).toMatchObject([
    { id: "sent", at: 6150, label: "响应发送结束" },
  ]);
});

it("keeps retries and hedges separate and never borrows the final request's first text", () => {
  const additional = timingRun.events
    .filter((e) => e.attempt_id)
    .map((e) => ({
      ...e,
      event_id: `hedge-${e.event_id}`,
      attempt_id: "b",
      at_ms: e.at_ms + 500,
      started_ms: e.started_ms == null ? null : e.started_ms + 500,
      detail: e.kind === "billing" ? { headers_at_ms: 1800 } : {},
      first_text_ms: null,
      transport: { ...e.transport, last_upstream_chunk_ms: undefined },
      outcome: e.kind === "attempt" ? "hedge_cancelled" : e.outcome,
    }));
  const trace = buildTrace({
    ...timingRun,
    events: [...timingRun.events, ...additional],
  });
  expect(trace.attempts).toHaveLength(2);
  const first = attemptTiming(trace.attempts[0]);
  const second = attemptTiming(trace.attempts[1]);
  expect(first.milestones.find((m) => m.id === "first-text")?.at).toBe(2100);
  expect(second.milestones.find((m) => m.id === "headers")?.at).toBe(1800);
  expect(second.milestones.find((m) => m.id === "first-text")).toBeUndefined();
  expect(second.missing).toEqual(["首字时间未记录", "上游末块时间未记录"]);
  expect(second.segments.some((s) => s.phase === "text")).toBe(false);
});

it("does not manufacture missing timestamps, an SSE ending, or non-finite positions", () => {
  const orphan = buildTrace({
    ...timingRun,
    events: [
      event({
        kind: "billing",
        first_text_ms: 100,
        response_created_ms: -1,
        transport: {
          first_upstream_chunk_ms: Infinity,
          last_upstream_chunk_ms: "42",
        },
      }),
    ],
  });
  expect(attemptTiming(orphan.attempts[0])).toEqual({
    milestones: [],
    segments: [],
    missing: ["首字时间无法定位（缺少渠道起点）", "上游末块时间未记录"],
  });
  const invalid = buildTrace({
    ...timingRun,
    events: [
      event({ kind: "dispatch" }),
      event({
        kind: "attempt",
        at_ms: 2000,
        first_text_ms: 5000,
        transport: { headers_received_ms: -4 },
      }),
    ],
  });
  const result = attemptTiming(invalid.attempts[0]);
  expect(result.milestones.map((m) => m.id)).toEqual(["start", "end"]);
  expect(result.segments).toEqual([]);
});

it("supports a zero offset and keeps downstream failure distinct from upstream completion", () => {
  const trace = buildTrace({
    ...timingRun,
    events: [
      event({ kind: "dispatch" }),
      event({
        kind: "attempt",
        at_ms: 2000,
        first_text_ms: 0,
        transport: { headers_received_ms: 0, first_upstream_chunk_ms: 0 },
      }),
      event({
        stage: "downstream_closed",
        at_ms: 2010,
        attempt_id: "",
        provider: "",
      }),
    ],
  });
  expect(
    attemptTiming(trace.attempts[0]).milestones.find(
      (m) => m.id === "first-text",
    )?.at,
  ).toBe(1000);
  expect(downstreamTiming(trace.gateway)).toMatchObject([
    { label: "下游连接关闭", at: 2010 },
  ]);
});
