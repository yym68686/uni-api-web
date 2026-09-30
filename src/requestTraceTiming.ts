import type { TraceAttempt, TraceEvent } from "./requestTraceModel";

export interface TraceMilestone {
  id: string;
  label: string;
  at: number;
  description: string;
}
export interface TraceSegment {
  phase: "waiting" | "responding" | "text";
  label: string;
  from: number;
  to: number;
}

const finite = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

export function attemptTiming(attempt: TraceAttempt) {
  const events = attempt.events;
  const milestones: TraceMilestone[] = [];
  const transport = (key: string) =>
    events.map((e) => e.transport[key]).find(finite);
  const metric = (
    key: "response_created_ms" | "first_text_ms" | "first_output_ms",
  ) => events.map((e) => e[key]).find(finite);
  const headersAt = events.map((e) => e.detail.headers_at_ms).find(finite);
  const headersElapsed = transport("headers_received_ms");
  // Transport offsets begin at the upstream HTTP send, not request ingress or
  // retry selection. A recorded header pair aligns that clock to wall time.
  const origin =
    headersAt != null && headersElapsed != null
      ? headersAt - headersElapsed
      : attempt.hasStart
        ? attempt.started
        : null;
  const add = (
    id: string,
    label: string,
    at: number | null | undefined,
    description: string,
  ) => {
    if (
      !finite(at) ||
      at > attempt.last + 1 ||
      (origin != null && at < origin - 1)
    )
      return;
    milestones.push({ id, label, at, description });
  };
  const offset = (value: number | undefined) =>
    origin != null && value != null ? origin + value : null;

  if (attempt.hasStart)
    add("start", "发起请求", attempt.started, "网关实际派发本次渠道请求。");
  add(
    "headers",
    "响应头",
    headersAt ?? offset(headersElapsed),
    "网关收到上游 HTTP 响应头；此时可能还没有正文。",
  );
  add(
    "first-chunk",
    "首块数据",
    offset(transport("first_upstream_chunk_ms")),
    "网关首次收到非空上游数据，可能只是 SSE 心跳或元数据。",
  );
  add(
    "created",
    "响应创建",
    offset(metric("response_created_ms")),
    "观测到 response.created 事件，区别于首个正文字符。",
  );
  add(
    "stream-ready",
    "流式就绪",
    offset(transport("public_stream_ready_ms")),
    "网关已准备好对外提供流式响应。",
  );
  add(
    "first-wire",
    "首个下行数据就绪",
    offset(transport("first_wire_prepared_ms")),
    "首个下行数据块已准备好，不代表客户端已经收到。",
  );
  add(
    "first-output",
    "首个有效输出",
    offset(metric("first_output_ms")),
    "首次观测到有效输出，可能是正文、推理或工具调用；只有正文增量才标为首字。",
  );
  add(
    "first-text",
    "首个正文",
    offset(metric("first_text_ms")),
    "观测到首个非空正文增量（首字），不将心跳、响应创建或推理内容算作正文。",
  );
  add(
    "last-chunk",
    "上游末块",
    offset(transport("last_upstream_chunk_ms")),
    "最后收到非空上游数据块的时间；数据块可能包含多条 SSE，不等同于独立采集的最后一条 SSE 时间。",
  );
  const result = events
    .filter((e) => e.kind === "attempt" || e.stage === "upstream_attempt")
    .at(-1);
  if (result)
    add(
      "end",
      "尝试结束",
      result.at_ms,
      "本次渠道尝试的结果记录时间，不等同于最后一条 SSE 到达时间或客户端接收完成。",
    );
  milestones.sort((a, b) => a.at - b.at);

  const firstResponse = milestones.find((m) =>
    ["headers", "first-chunk", "created", "first-text"].includes(m.id),
  );
  const firstText = milestones.find((m) => m.id === "first-text");
  const end = attempt.end ?? attempt.last;
  const segments: TraceSegment[] = [];
  const segment = (
    phase: TraceSegment["phase"],
    label: string,
    from: number,
    to: number,
  ) => {
    if (to > from) segments.push({ phase, label, from, to });
  };
  // Only assign phases when the boundary was actually observed.
  if (attempt.hasStart && firstResponse) {
    segment("waiting", "等待上游响应", attempt.started, firstResponse.at);
    if (firstText && firstText.at >= firstResponse.at) {
      segment(
        "responding",
        "响应已开始，等待正文",
        firstResponse.at,
        firstText.at,
      );
      segment("text", "首个正文之后", firstText.at, end);
    } else {
      segment("responding", "响应已开始", firstResponse.at, end);
    }
  }
  const missing: string[] = [];
  if (!firstText)
    missing.push(
      metric("first_text_ms") == null
        ? "首字时间未记录"
        : origin == null
          ? "首字时间无法定位（缺少渠道起点）"
          : "首字时间超出记录范围",
    );
  if (
    events.some((e) => e.stream) &&
    !milestones.some((m) => m.id === "last-chunk")
  )
    missing.push("上游末块时间未记录");
  return { milestones, segments, missing };
}

export function downstreamTiming(events: TraceEvent[]): TraceMilestone[] {
  const labels: Record<string, [string, string]> = {
    response_headers: ["返回响应头", "网关开始向下游返回响应头。"],
    response_body_finished: [
      "响应发送结束",
      "网关响应体输出结束；不证明客户端已接收全部字节，也不是上游最后一条 SSE 的采集时间。",
    ],
    response_body_error: ["响应传输失败", "网关向下游输出响应体时发生错误。"],
    downstream_closed: ["下游连接关闭", "下游连接提前关闭，不能视为正常完成。"],
  };
  return events
    .filter((e) => labels[e.stage] && finite(e.at_ms))
    .map((e) => ({
      id: e.event_id,
      label: labels[e.stage][0],
      description: labels[e.stage][1],
      at: e.at_ms,
    }));
}
