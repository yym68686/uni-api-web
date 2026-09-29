import { useState } from "react";
import type { FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search, RefreshCw } from "lucide-react";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, Metrics } from "./types";
import { Spinner } from "./ui";
import "./requestTrace.css";

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
  detail: Record<string, unknown>;
  transport: Record<string, unknown>;
}
export interface TraceRun {
  source_id: string;
  instance_id: string;
  request_id: string;
  ambiguous: boolean;
  events: TraceEvent[];
}
interface TraceResult {
  request_id: string;
  data: TraceRun[];
  import?: Metrics["import"];
  generated_at: number;
}
const stages: Record<string, string> = {
  request_received: "请求进入网关",
  rust_request_spool: "请求体读取 / 资源等待",
  routing_attempt: "路由选择 / 重试决策",
  dispatch: "发起渠道请求",
  billing: "渠道 HTTP 响应",
  attempt: "渠道尝试结束",
  upstream_attempt: "渠道尝试结果",
  request: "最终请求结果",
  response_headers: "向客户端提交响应头",
  response_body_finished: "响应体发送结束",
  response_body_error: "响应体传输错误",
  downstream_closed: "连接提前关闭 / 处理被取消",
  gateway_error: "向客户端返回错误",
  responses_empty_name_repair: "修复空工具名后重试",
  responses_missing_item_repair: "修复缺失推理项后重试",
  responses_encrypted_content_repair: "修复加密历史后重试",
  responses_heartbeat_repair: "修复心跳历史后重试",
};
const fields: Record<string, string> = {
  headers_received_ms: "收到上游响应头",
  first_upstream_chunk_ms: "收到上游首块数据",
  public_stream_ready_ms: "流式响应就绪",
  first_wire_prepared_ms: "首个下行数据就绪",
  preflight_decode_ms: "预检解析耗时",
  preflight_read_wait_ms: "预检读取等待",
  preflight_process_ms: "预检处理耗时",
  error_body_read_ms: "错误响应读取耗时",
  resource_wait_ms: "资源等待",
  duration_ms: "本阶段耗时",
  attempt_index: "尝试序号",
  attempt_outcome: "尝试结果",
  skip_reason: "跳过原因",
  status_origin: "状态产生阶段",
  error_code: "错误代码",
  error_type: "错误类型",
  error_message: "错误信息（脱敏）",
  semantic_status_code: "语义状态码",
  attempt_status_code: "渠道状态码",
  failure_resource: "资源错误",
  body_bytes: "请求体字节数",
};
function ms(value: number | null | undefined) {
  return value == null ? "未记录" : `${value.toFixed(1)} ms`;
}
function clock(value: number) {
  return (
    new Date(value).toLocaleString("zh-CN", { hour12: false }) +
    "." +
    String(value % 1000).padStart(3, "0")
  );
}
function errorText(e: TraceEvent) {
  const error = e.detail.error as Record<string, unknown> | undefined;
  return (
    [
      error?.error_code,
      error?.error_message,
      e.detail.error_code,
      e.detail.error_message,
      e.failure_reason,
      e.detail.skip_reason,
    ]
      .filter((v) => typeof v === "string" && v)
      .join(" · ") || ""
  );
}
function statusText(e: TraceEvent) {
  const status = e.status || Number(e.detail.attempt_status_code || 0);
  return (
    [
      status ? `HTTP ${status}` : "",
      e.outcome || e.detail.attempt_outcome || e.terminal_kind,
    ]
      .filter(Boolean)
      .join(" · ") || "已记录"
  );
}
function EventDetails({ event: e }: { event: TraceEvent }) {
  const details = {
    ...e.transport,
    ...e.detail,
    ...((e.detail.error as object) || {}),
  };
  return (
    <details>
      <summary>阶段时间与详情</summary>
      <dl className="request-trace-details">
        <dt>绝对时间</dt>
        <dd>{clock(e.at_ms)}</dd>
        {e.started_ms != null && (
          <>
            <dt>渠道开始时间</dt>
            <dd>{clock(e.started_ms)}</dd>
          </>
        )}
        <dt>记录耗时</dt>
        <dd>{ms(e.duration_ms)}</dd>
        {e.dispatch_ms != null && (
          <>
            <dt>进入网关至本次派发</dt>
            <dd>{ms(e.dispatch_ms)}</dd>
          </>
        )}
        {e.response_created_ms != null && (
          <>
            <dt>渠道响应创建</dt>
            <dd>{ms(e.response_created_ms)}</dd>
          </>
        )}
        {e.first_text_ms != null && (
          <>
            <dt>渠道首个正文</dt>
            <dd>{ms(e.first_text_ms)}</dd>
          </>
        )}
        {Object.entries(fields).map(([key, label]) =>
          details[key] == null ? null : (
            <div className="trace-field" key={key}>
              <dt>{label}</dt>
              <dd>
                {key.endsWith("_ms")
                  ? ms(Number(details[key]))
                  : String(details[key])}
              </dd>
            </div>
          ),
        )}
        <dt>尝试 ID</dt>
        <dd>{e.attempt_id || "—"}</dd>
        <dt>上游模型</dt>
        <dd>{e.upstream_model || "未记录"}</dd>
        {Object.keys(e.transport).length > 0 && (
          <>
            <dt>渠道时钟起点</dt>
            <dd>{String(e.transport.origin || "上游 HTTP 请求开始")}</dd>
          </>
        )}
      </dl>
    </details>
  );
}

export function TraceTimeline({
  run,
  sourceName,
}: {
  run: TraceRun;
  sourceName: string;
}) {
  const arrival = run.events.find((e) => e.stage === "request_received");
  const finals = run.events.filter((e) => e.kind === "request");
  const final = run.ambiguous ? undefined : finals.at(-1);
  const last = run.events.at(-1)!;
  const transportFinished = run.events.find(
    (e) => e.stage === "response_body_finished",
  );
  const attempts = new Set(
    run.events
      .filter((e) => e.kind === "dispatch" || e.kind === "attempt")
      .map((e) => e.attempt_id)
      .filter(Boolean),
  );
  const model = final?.model || run.events.find((e) => e.model)?.model;
  const start = arrival?.at_ms;
  return (
    <section className="trace-run">
      <div className="trace-run-heading">
        <h3>{sourceName}</h3>
        <span className="mono">{run.request_id}</span>
      </div>
      {run.ambiguous && (
        <p className="error-banner">
          同一实例重复使用了此请求
          ID，存在多个请求。以下为全部匹配事件，不能把它们当作一次请求的重试链。
        </p>
      )}
      <div className="trace-summary">
        <div>
          <small>最终状态</small>
          <strong>
            {final
              ? statusText(final)
              : transportFinished
                ? `HTTP ${transportFinished.status} · 响应传输结束`
                : "未记录最终结果"}
          </strong>
        </div>
        <div>
          <small>模型</small>
          <strong>{model || "未记录"}</strong>
        </div>
        <div>
          <small>已记录渠道尝试</small>
          <strong>{attempts.size}</strong>
        </div>
        <div>
          <small>网关总耗时</small>
          <strong>
            {run.ambiguous
              ? "无法区分"
              : transportFinished
                ? ms(transportFinished.duration_ms)
                : final
                  ? ms(final.duration_ms)
                  : "未记录"}
          </strong>
        </div>
      </div>
      {!arrival && (
        <p className="muted">
          历史记录未保存入口事件；以下仅展示已采集到的事实，不推算缺失的阶段或错误原文。
        </p>
      )}
      {arrival && !final && (
        <p className="muted">
          已记录网关入口，但最终业务结果尚未同步或未被采集；HTTP
          响应头不代表模型处理成功。
        </p>
      )}
      <div className="table-scroll">
        <table className="channel-table request-trace-table">
          <thead>
            <tr>
              <th>时间 / 相对入口</th>
              <th>行为</th>
              <th>渠道 / 尝试</th>
              <th>状态与错误</th>
              <th>耗时与详情</th>
            </tr>
          </thead>
          <tbody>
            {run.events.map((e) => (
              <tr key={e.event_id}>
                <td>
                  <time>
                    {new Date(e.at_ms).toLocaleTimeString("zh-CN", {
                      hour12: false,
                    })}
                    .{String(e.at_ms % 1000).padStart(3, "0")}
                  </time>
                  <small>
                    {start == null || run.ambiguous
                      ? "相对时间未记录"
                      : `+${ms(Math.max(0, e.at_ms - start))}`}
                  </small>
                </td>
                <td>
                  <strong>
                    {stages[e.stage || e.kind] || e.stage || e.kind}
                  </strong>
                </td>
                <td>
                  {e.provider || "网关"}
                  <small className="mono">{e.attempt_id || "—"}</small>
                </td>
                <td>
                  {statusText(e)}
                  {errorText(e) && (
                    <small className="trace-error">{errorText(e)}</small>
                  )}
                </td>
                <td>
                  <EventDetails event={e} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted trace-footnote">
        实例：{run.instance_id || "历史记录未提供"} · 最近事件：
        {clock(last.at_ms)}
        。渠道阶段时间从各自请求开始计时；并发尝试的耗时不可直接相加。响应体发送结束表示网关完成交付，不证明客户端已接收全部字节。
      </p>
    </section>
  );
}

export function RequestTracePage({
  connection,
  sources,
}: {
  connection: Connection;
  sources: { id: string; name: string }[];
}) {
  const [id, setId] = useState("");
  const [source, setSource] = useState("");
  const [search, setSearch] = useState<{ id: string; source: string } | null>(
    null,
  );
  const query = useQuery({
    queryKey: ["request-trace", connection.session, search?.source, search?.id],
    queryFn: ({ signal }) =>
      analyticsRequest<TraceResult>(
        connection,
        "/analytics/v1/request-trace?" +
          new URLSearchParams({
            request_id: search!.id,
            source_id: search!.source,
          }),
        signal,
      ),
    enabled: !!search,
    retry: false,
    refetchInterval: initializationRetryInterval,
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const value = id.trim();
    if (!value) return;
    if (search?.id === value && search.source === source) void query.refetch();
    else setSearch({ id: value, source });
  }
  return (
    <section className="data-panel request-trace-panel">
      <div className="panel-head">
        <h2>请求追踪</h2>
      </div>
      <p className="muted">
        输入 Request ID，查看请求进入 uni-api
        后的渠道选择、重试、错误、阶段时间和最终结果。
      </p>
      <form className="trace-search" onSubmit={submit}>
        <label>
          Request ID
          <input
            aria-label="请求 ID"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="粘贴请求 ID"
            maxLength={512}
            required
          />
        </label>
        {!!sources.length && (
          <label>
            来源
            <select
              aria-label="请求追踪来源"
              value={source}
              onChange={(e) => setSource(e.target.value)}
            >
              <option value="">全部来源</option>
              {sources.map((s) => (
                <option value={s.id} key={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <button
          className="button primary"
          disabled={!id.trim() || query.isFetching}
        >
          {query.isFetching ? <Spinner small /> : <Search size={16} />}查询请求
        </button>
        {search && (
          <button
            className="button"
            type="button"
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            <RefreshCw size={16} />
            刷新记录
          </button>
        )}
      </form>
      {query.isFetching && (
        <p role="status">
          <Spinner small />
          正在读取请求记录…
        </p>
      )}
      {query.error && (
        <p role="alert" className="error-banner">
          {query.error.message}
        </p>
      )}
      {query.data?.import?.caught_up === false && (
        <p className="coverage-note">
          历史事实仍在同步，当前链路可能不完整。稍后可刷新记录。
        </p>
      )}
      {query.data?.data.length === 0 && (
        <div className="trace-empty">
          <h3>未找到该请求的网关记录</h3>
          <p>
            请核对请求 ID 与来源。请求可能尚未同步、发生于采集启用之前，或在到达
            uni-api
            之前已被上层服务拒绝。仅凭没有记录，不能判断请求是否进入过网关。
          </p>
        </div>
      )}
      {query.data?.data.map((run) => (
        <TraceTimeline
          key={`${run.source_id}:${run.instance_id}:${run.request_id}`}
          run={run}
          sourceName={
            sources.find((s) => s.id === run.source_id)?.name || run.source_id
          }
        />
      ))}
    </section>
  );
}
