export interface TraceEvent {
  event_id: string;
  kind: string;
  stage: string;
  at_ms: number;
  started_ms: number | null;
  attempt_id: string;
  provider: string;
  model: string;
  upstream_model: string;
  endpoint: string;
  stream: boolean;
  outcome: string;
  status: number;
  terminal_kind: string;
  failure_reason: string;
  response_completed: boolean | null;
  duration_ms: number | null;
  dispatch_ms: number | null;
  response_created_ms: number | null;
  first_text_ms: number | null;
  first_output_ms?: number | null;
  detail: Record<string, unknown>;
  transport: Record<string, unknown>;
}
export interface TraceChannel {
  name: string;
  site_name?: string;
  group_name?: string;
  group_id?: number;
  rate?: number | null;
  dashboard_url?: string;
}
export interface TraceRun {
  source_id: string;
  instance_id: string;
  request_id: string;
  ambiguous: boolean;
  events: TraceEvent[];
  channels?: Record<string, TraceChannel>;
}
export type TraceTone = "success" | "failure" | "neutral" | "warning";
export interface TraceAttempt {
  id: string;
  provider: string;
  events: TraceEvent[];
  first: number;
  last: number;
  hasStart: boolean;
  started: number;
  end: number | null;
  duration: number | null;
  result?: TraceEvent;
  status: number;
  label: string;
  tone: TraceTone;
  errors: string[];
}
export const traceStages: Record<string, string> = {
  request_received: "请求进入网关",
  rust_request_spool: "读取请求 / 等待资源",
  routing_attempt: "选择渠道 / 重试决策",
  dispatch: "发起渠道请求",
  billing: "上游响应与错误",
  attempt: "渠道尝试结束",
  upstream_attempt: "渠道尝试结果",
  request: "请求最终结果",
  response_headers: "返回响应头",
  response_body_finished: "响应发送结束",
  response_body_error: "响应传输失败",
  downstream_closed: "下游连接关闭",
  gateway_error: "网关返回错误",
  responses_empty_name_repair: "修复空工具名后重试",
  responses_missing_item_repair: "修复缺失推理项后重试",
  responses_encrypted_content_repair: "修复加密历史后重试",
  responses_heartbeat_repair: "修复心跳历史后重试",
};
export function duration(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "未记录";
  return value < 1000
    ? `${value.toFixed(1)} ms`
    : value < 60000
      ? `${(value / 1000).toFixed(2)} s`
      : `${(value / 60000).toFixed(2)} min`;
}
export function eventHTTP(e?: TraceEvent) {
  return e
    ? e.status ||
        Number(
          e.detail.attempt_status_code || e.detail.semantic_status_code || 0,
        )
    : 0;
}
export function eventResult(e?: TraceEvent): {
  label: string;
  tone: TraceTone;
} {
  if (!e) return { label: "结果未记录", tone: "neutral" };
  const outcome = String(
    e.outcome || e.detail.attempt_outcome || e.terminal_kind || "",
  );
  if (
    e.response_completed === false ||
    /^(failed|error)|http_error|stream_error/.test(outcome) ||
    eventHTTP(e) >= 400 ||
    e.stage === "response_body_error" ||
    e.stage === "gateway_error"
  )
    return { label: "失败", tone: "failure" };
  if (outcome.includes("cancel") || e.stage === "downstream_closed")
    return {
      label: outcome === "hedge_cancelled" ? "竞速取消" : "已取消",
      tone: "warning",
    };
  if (outcome === "skipped" || e.detail.skip_reason)
    return { label: "已跳过", tone: "warning" };
  if (
    outcome === "success" ||
    outcome === "completed" ||
    e.response_completed === true
  )
    return { label: "成功", tone: "success" };
  if (e.stage === "response_body_finished")
    return { label: "响应发送结束", tone: "neutral" };
  return { label: eventHTTP(e) ? "收到响应" : "已记录", tone: "neutral" };
}
const reasons: Record<string, string> = {
  INSUFFICIENT_BALANCE: "账号余额不足",
  insufficient_balance: "账号余额不足",
  upstream_http_403: "上游拒绝访问",
  upstream_http_429: "上游限流",
  upstream_http_521: "上游服务不可用",
  timeout: "请求超时",
  channel_cooldown: "渠道冷却中",
  no_provider_key: "没有可用密钥",
  temporarily_disabled: "渠道已停用",
  other: "其他错误",
};
export function friendlyReason(value: string) {
  const key = value.replace(/^failed\//, "");
  return (
    reasons[key] ||
    (/^upstream_http_\d+$/.test(key)
      ? `上游返回 HTTP ${key.slice("upstream_http_".length)}`
      : value)
  );
}
export function eventErrors(e: TraceEvent) {
  const error = e.detail.error as Record<string, unknown> | undefined;
  const code = error?.error_code || e.detail.error_code;
  const message = error?.error_message || e.detail.error_message;
  const values: string[] = [];
  if (typeof code === "string" && code) values.push(friendlyReason(code));
  if (typeof message === "string" && message) values.push(message);
  if (!values.length) {
    const reason =
      e.failure_reason ||
      String(e.detail.skip_reason || "") ||
      (e.outcome.startsWith("failed/") ? e.outcome.slice(7) : "");
    if (reason) values.push(friendlyReason(reason));
  }
  return [...new Set(values)].join(" · ");
}
const gatewayStages = new Set([
  "request_received",
  "rust_request_spool",
  "request",
  "response_headers",
  "response_body_finished",
  "response_body_error",
  "downstream_closed",
  "gateway_error",
]);
export function buildTrace(run: TraceRun) {
  const events = [...run.events].sort((a, b) => a.at_ms - b.at_ms);
  const arrival = run.ambiguous
    ? undefined
    : events.find((e) => e.stage === "request_received");
  const final = run.ambiguous
    ? undefined
    : events.filter((e) => e.kind === "request").at(-1);
  const completed = run.ambiguous
    ? undefined
    : events.filter((e) => e.stage === "response_body_finished").at(-1);
  const groups = new Map<string, TraceEvent[]>();
  const providersByAttempt = new Map<string, Set<string>>();
  for (const e of events)
    if (e.attempt_id && e.provider) {
      const providers =
        providersByAttempt.get(e.attempt_id) || new Set<string>();
      providers.add(e.provider);
      providersByAttempt.set(e.attempt_id, providers);
    }
  const gateway: TraceEvent[] = [];
  for (const e of events) {
    if (
      e.kind === "request" ||
      gatewayStages.has(e.stage) ||
      (!e.provider && !e.attempt_id)
    ) {
      gateway.push(e);
      continue;
    }
    // Ambiguous request IDs cannot safely coalesce repeated attempt IDs.
    const id = run.ambiguous ? e.event_id : e.attempt_id || e.event_id;
    const providers = providersByAttempt.get(e.attempt_id);
    const provider =
      e.provider || (providers?.size === 1 ? [...providers][0] : "");
    const key = JSON.stringify([id, provider]);
    const group = groups.get(key) || [];
    group.push(e);
    groups.set(key, group);
  }
  const attempts: TraceAttempt[] = [...groups.entries()]
    .map(([id, events]) => {
      const first = events[0].at_ms,
        last = events.at(-1)!.at_ms;
      const dispatch = events.find((e) => e.kind === "dispatch");
      const outcome = events
        .filter((e) => e.kind === "attempt" || e.stage === "upstream_attempt")
        .at(-1);
      const billing = events.filter((e) => e.kind === "billing").at(-1);
      const result =
        outcome || billing || events.find((e) => e.detail.skip_reason);
      const recordedStart = events
        .map((e) => e.started_ms)
        .find((t) => t != null && t > 0 && t <= last);
      const started = dispatch?.at_ms ?? recordedStart ?? first;
      const end = result && result.at_ms >= started ? result.at_ms : null;
      const elapsed =
        outcome?.duration_ms ??
        (end != null && (dispatch || recordedStart != null)
          ? end - started
          : null);
      return {
        id,
        provider: events.find((e) => e.provider)?.provider || "",
        events,
        first,
        last,
        started,
        hasStart: !!dispatch || recordedStart != null,
        end,
        duration: elapsed,
        result,
        ...eventResult(result),
        status: eventHTTP(billing) || eventHTTP(result),
        errors: [...new Set(events.map(eventErrors).filter(Boolean))],
      };
    })
    .sort((a, b) => a.started - b.started);
  const first = events.length
    ? attempts.reduce((min, a) => Math.min(min, a.started), events[0].at_ms)
    : 0;
  const origin = arrival?.at_ms ?? first;
  const end = events.reduce((max, e) => Math.max(max, e.at_ms), origin);
  return {
    events,
    arrival,
    final,
    completed,
    attempts,
    gateway,
    origin,
    end,
    span: Math.max(1, end - origin),
  };
}
export function traceChannel(run: TraceRun, provider: string): TraceChannel {
  const channel = run.channels?.[provider];
  if (channel?.site_name)
    return {
      ...channel,
      name:
        channel.rate != null
          ? `${channel.site_name} · ${channel.rate}×`
          : `${channel.site_name} · 倍率未知`,
    };
  const name = channel?.name || provider;
  if (!name) return { name: "网关" };
  if (/^sub2api-(copy-)?[a-f0-9]+$/i.test(name)) {
    const providers = [
      ...new Set(run.events.map((e) => e.provider).filter(Boolean)),
    ];
    return {
      ...channel,
      name: `未关联渠道 ${providers.indexOf(provider) + 1}`,
    };
  }
  return { ...channel, name };
}
