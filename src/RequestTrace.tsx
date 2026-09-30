import { useState } from "react";
import type { CSSProperties } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { queryOptions, useQuery } from "@tanstack/react-query";
import {
  RefreshCw,
  ArrowUpRight,
  Check,
  X,
  Clock3,
  Route,
  Activity,
} from "lucide-react";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, Metrics } from "./types";
import { Spinner } from "./ui";
import { SiteLink } from "./ChannelSite";
import {
  buildTrace,
  duration,
  eventHTTP,
  eventResult,
  eventErrors,
  traceChannel,
  traceStages,
} from "./requestTraceModel";
import type {
  TraceRun,
  TraceEvent,
  TraceAttempt,
  TraceTone,
} from "./requestTraceModel";
import { attemptTiming, downstreamTiming } from "./requestTraceTiming";
import type { TraceMilestone } from "./requestTraceTiming";
export type { TraceRun, TraceEvent } from "./requestTraceModel";
import "./requestTrace.css";

export function requestTraceOptions(
  connection: Connection,
  requestId: string,
  sourceId: string,
  instanceId?: string,
) {
  return queryOptions({
    queryKey: [
      "request-trace",
      connection.base,
      connection.session,
      sourceId,
      instanceId,
      requestId,
    ],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({
        request_id: requestId,
        source_id: sourceId,
      });
      if (instanceId !== undefined) params.set("instance_id", instanceId);
      return analyticsRequest<TraceResult>(
        connection,
        "/analytics/v1/request-trace?" + params,
        signal,
      );
    },
    retry: false,
    // Incomplete imports still revalidate on opening; complete historical
    // traces can reuse intent prefetches and quick repeat visits.
    staleTime: (query) =>
      query.state.data?.data.length &&
      query.state.data.import?.caught_up !== false
        ? 30_000
        : 0,
    refetchInterval: initializationRetryInterval,
  });
}

export function RequestTraceDialog({
  connection,
  sources,
  requestId,
  sourceId,
  instanceId,
  onClose,
}: {
  connection: Connection;
  sources: { id: string; name: string }[];
  requestId: string;
  sourceId: string;
  instanceId?: string;
  onClose: () => void;
}) {
  const query = useQuery(
    requestTraceOptions(connection, requestId, sourceId, instanceId),
  );
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="request-log-dialog">
          <header className="request-log-dialog-heading">
            <div>
              <Dialog.Title>请求详情</Dialog.Title>
              <Dialog.Description>{requestId}</Dialog.Description>
            </div>
            <button
              className="button small"
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <RefreshCw size={15} />
              刷新记录
            </button>
            <Dialog.Close className="icon-button" aria-label="关闭请求详情">
              <X size={20} />
            </Dialog.Close>
          </header>
          <div className="request-log-dialog-body">
            {query.isPending && (
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
                  请核对请求 ID 与来源。记录可能尚未同步或发生于采集启用之前。
                </p>
              </div>
            )}
            {query.data?.data.map((run) => (
              <TraceTimeline
                key={`${run.source_id}:${run.instance_id}:${run.request_id}`}
                run={run}
                sourceName={
                  sources.find((s) => s.id === run.source_id)?.name ||
                  run.source_id
                }
              />
            ))}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
interface TraceResult {
  request_id: string;
  data: TraceRun[];
  import?: Metrics["import"];
  generated_at: number;
}
const fields: Record<string, string> = {
  headers_received_ms: "收到上游响应头",
  first_upstream_chunk_ms: "收到上游首块数据",
  public_stream_ready_ms: "流式响应就绪",
  first_wire_prepared_ms: "首个下行数据就绪",
  last_upstream_chunk_ms: "收到上游末块数据",
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
function EventDetails({ event: e }: { event: TraceEvent }) {
  const details = {
    ...e.transport,
    ...e.detail,
    ...((e.detail.error as object) || {}),
  };
  return (
    <details>
      <summary>查看阶段详情</summary>
      <dl className="request-trace-details">
        <dt>绝对时间</dt>
        <dd>{clock(e.at_ms)}</dd>
        {typeof e.detail.headers_at_ms === "number" && (
          <>
            <dt>收到上游响应头</dt>
            <dd>{clock(e.detail.headers_at_ms)}</dd>
          </>
        )}
        {e.started_ms != null && e.started_ms > 0 && (
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
        <dt>内部渠道 ID</dt>
        <dd>{e.provider || "—"}</dd>
        <dt>原始阶段</dt>
        <dd>{e.stage || e.kind}</dd>
        <dt>原始状态</dt>
        <dd>
          {e.outcome ||
            (e.detail.attempt_outcome as string) ||
            e.terminal_kind ||
            "未记录"}
        </dd>
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

function Status({
  tone,
  label,
  status,
}: {
  tone: TraceTone;
  label: string;
  status?: number;
}) {
  return (
    <span className={`trace-status ${tone}`}>
      {tone === "success" ? (
        <Check size={13} />
      ) : tone === "failure" ? (
        <X size={13} />
      ) : (
        <Clock3 size={13} />
      )}{" "}
      {label}
      {!!status && <span>HTTP {status}</span>}
    </span>
  );
}
function StageList({
  events,
  origin,
}: {
  events: TraceEvent[];
  origin: number;
}) {
  return (
    <ol className="trace-stage-list">
      {events.map((e) => (
        <li key={e.event_id}>
          <span className={`trace-stage-dot ${eventResult(e).tone}`} />
          <div className="trace-stage-title">
            <strong>{traceStages[e.stage || e.kind] || "其他阶段"}</strong>
            <time title={clock(e.at_ms)}>
              +{duration(Math.max(0, e.at_ms - origin))}
            </time>
          </div>
          {(e.outcome || eventHTTP(e) > 0) && (
            <Status {...eventResult(e)} status={eventHTTP(e)} />
          )}
          {eventErrors(e) && <p className="trace-error">{eventErrors(e)}</p>}
          <EventDetails event={e} />
        </li>
      ))}
    </ol>
  );
}

function TimingMilestones({
  milestones,
  origin,
  started,
  missing = [],
}: {
  milestones: TraceMilestone[];
  origin: number;
  started?: number;
  missing?: string[];
}) {
  return (
    <div className="trace-timing-details">
      <dl className="trace-milestone-list">
        {milestones.map((point) => (
          <div key={point.id} title={point.description}>
            <dt>{point.label}</dt>
            <dd>
              <strong>+{duration(point.at - origin)}</strong>
              <time dateTime={new Date(point.at).toISOString()}>
                {clock(Math.round(point.at))}
              </time>
              {started != null && (
                <small>本次渠道 +{duration(point.at - started)}</small>
              )}
            </dd>
          </div>
        ))}
      </dl>
      {missing.length > 0 && (
        <p className="trace-timing-missing">{missing.join(" · ")}</p>
      )}
    </div>
  );
}

function AttemptWaterfall({
  run,
  attempt: a,
  index,
  origin,
  span,
  selected,
  onOpen,
}: {
  run: TraceRun;
  attempt: TraceAttempt;
  index: number;
  origin: number;
  span: number;
  selected: boolean;
  onOpen: () => void;
}) {
  const { milestones, segments, missing } = attemptTiming(a);
  const [activeID, setActiveID] = useState<string | null>(null);
  const active =
    milestones.find((m) => m.id === activeID) ||
    milestones.find((m) => m.id === "first-text") ||
    milestones.find((m) => m.id === "first-output") ||
    milestones.find((m) => m.id === "headers");
  const position = (at: number) =>
    Math.max(0, Math.min(100, ((at - origin) / span) * 100));
  const markers: { at: number; points: TraceMilestone[] }[] = [];
  for (const point of milestones) {
    const last = markers.at(-1);
    if (last && position(point.at) - position(last.at) < 4)
      last.points.push(point);
    else markers.push({ at: point.at, points: [point] });
  }
  return (
    <div className={`trace-waterfall-row ${selected ? "selected" : ""}`}>
      <button
        className="trace-channel-link"
        onClick={onOpen}
        title={traceChannel(run, a.provider).group_name}
      >
        <span className="trace-attempt-number">{index + 1}</span>
        <span>{traceChannel(run, a.provider).name}</span>
        <ArrowUpRight size={13} />
      </button>
      <button
        className="trace-lane"
        aria-label={`查看第 ${index + 1} 次尝试：${traceChannel(run, a.provider).name}，${a.label}，${duration(a.duration)}`}
        onClick={onOpen}
      >
        <span
          className={`trace-duration-bar ${a.tone} ${a.end == null ? "incomplete" : ""} ${segments.length ? "segmented" : ""}`}
          style={
            {
              "--trace-left": `${position(a.started)}%`,
              "--trace-width": `${Math.max(0.6, position(a.end ?? a.last) - position(a.started))}%`,
            } as CSSProperties
          }
        />
        {segments.map((s) => (
          <span
            key={s.phase}
            className={`trace-phase-bar ${s.phase}`}
            aria-hidden="true"
            style={{
              left: `${position(s.from)}%`,
              width: `${position(s.to) - position(s.from)}%`,
            }}
          />
        ))}
        {markers.map((m) => (
          <span
            key={m.points[0].id}
            className="trace-time-marker"
            aria-hidden="true"
            style={{ left: `${position(m.at)}%` }}
          >
            <span>
              {m.points.length > 1
                ? `${m.points.length}点`
                : milestones.indexOf(m.points[0]) + 1}
            </span>
          </span>
        ))}
        {active && (
          <span
            className="trace-active-marker"
            aria-hidden="true"
            style={{ left: `${position(active.at)}%` }}
          />
        )}
      </button>
      <span className="trace-elapsed">
        {duration(a.duration)}
        <small className={a.tone}>{a.label}</small>
      </span>
      <div className="trace-waterfall-points">
        <div
          className="trace-milestone-chips"
          aria-label={`第 ${index + 1} 次尝试的时间节点`}
        >
          {milestones.map((m, i) => (
            <button
              key={m.id}
              className={`trace-milestone-chip ${m.id === "first-text" ? "first-text" : ""}`}
              aria-pressed={active?.id === m.id}
              onClick={() => setActiveID(m.id)}
              onFocus={() => setActiveID(m.id)}
            >
              <span>{i + 1}</span>
              {m.label}
              <strong>+{duration(m.at - origin)}</strong>
            </button>
          ))}
        </div>
        {active && (
          <p className="trace-active-time" role="status">
            <strong>{active.label}</strong> · {clock(Math.round(active.at))}
            {a.hasStart && ` · 本次渠道 +${duration(active.at - a.started)}`}
            <span>{active.description}</span>
          </p>
        )}
        {!!missing.length && (
          <p className="trace-timing-missing">{missing.join(" · ")}</p>
        )}
      </div>
    </div>
  );
}

function AttemptDrawer({
  run,
  attempt,
  number,
  origin,
  onClose,
}: {
  run: TraceRun;
  attempt: TraceAttempt;
  number: number;
  origin: number;
  onClose: () => void;
}) {
  const channel = traceChannel(run, attempt.provider);
  const timing = attemptTiming(attempt);
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay trace-attempt-overlay" />
        <Dialog.Content className="detail-panel trace-detail-drawer">
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭尝试详情"
          >
            <X size={20} />
          </Dialog.Close>
          <span className="eyebrow">
            CHANNEL ATTEMPT {number.toString().padStart(2, "0")}
          </span>
          <Dialog.Title>{channel.name}</Dialog.Title>
          <Dialog.Description>
            {channel.group_name || "本次请求的渠道尝试"}
            {channel.group_id ? ` · 分组 #${channel.group_id}` : ""}
          </Dialog.Description>
          <div className="trace-drawer-status">
            <Status
              tone={attempt.tone}
              label={attempt.label}
              status={attempt.status}
            />
            <strong>{duration(attempt.duration)}</strong>
          </div>
          {channel.dashboard_url && (
            <div className="trace-site-link">
              <SiteLink base={channel.dashboard_url}>打开站点控制台</SiteLink>
            </div>
          )}
          {channel.site_name && (
            <p className="muted">
              站点名称与倍率来自当前渠道配置，不代表请求发生时的计费倍率。
            </p>
          )}
          <section className="detail-section">
            <h3>响应时间节点</h3>
            <TimingMilestones
              {...timing}
              origin={origin}
              started={attempt.hasStart ? attempt.started : undefined}
            />
          </section>
          <section className="detail-section">
            <h3>阶段时间线</h3>
            <StageList events={attempt.events} origin={origin} />
          </section>
          <details className="trace-technical">
            <summary>技术标识</summary>
            <dl className="request-trace-details">
              <dt>来源</dt>
              <dd>{run.source_id}</dd>
              <dt>渠道 ID</dt>
              <dd>{attempt.provider}</dd>
              <dt>尝试 ID</dt>
              <dd>
                {attempt.events.find((e) => e.attempt_id)?.attempt_id ||
                  "未记录"}
              </dd>
              <dt>请求 ID</dt>
              <dd>{run.request_id}</dd>
            </dl>
          </details>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function TraceTimeline({
  run,
  sourceName,
}: {
  run: TraceRun;
  sourceName: string;
}) {
  const trace = buildTrace(run);
  const [selected, setSelected] = useState<string | null>(null);
  const [openAttempt, setOpenAttempt] = useState<string | null>(null);
  const { attempts, arrival, final, completed, origin, span } = trace;
  const finalState = run.ambiguous
    ? { label: "无法区分", tone: "warning" as const }
    : eventResult(final);
  const model = final?.model || trace.events.find((e) => e.model)?.model;
  const total = run.ambiguous
    ? null
    : (completed?.duration_ms ?? final?.duration_ms);
  const successes = attempts.filter((a) => a.tone === "success").length;
  const failures = attempts.filter((a) => a.tone === "failure").length;
  const chosen = attempts.find((a) => a.id === openAttempt);
  const hasTimes = !run.ambiguous && trace.events.length > 0;
  const downstream = run.ambiguous ? [] : downstreamTiming(trace.gateway);
  const open = (attempt: TraceAttempt) => {
    setSelected(attempt.id);
    setOpenAttempt(attempt.id);
  };
  return (
    <section className="trace-run">
      <div className="trace-run-heading">
        <div>
          <span className="trace-source">{sourceName}</span>
          <h3>
            {run.ambiguous
              ? "匹配到多个请求"
              : finalState.tone === "success"
                ? "请求成功"
                : finalState.tone === "failure"
                  ? "请求失败"
                  : "请求记录"}
          </h3>
        </div>
        <Status {...finalState} status={final ? eventHTTP(final) : undefined} />
      </div>
      <div className="trace-summary">
        <div>
          <small>请求模型</small>
          <strong>{model || "未记录"}</strong>
        </div>
        <div>
          <small>网关总耗时</small>
          <strong>{duration(total)}</strong>
        </div>
        <div>
          <small>{run.ambiguous ? "渠道事件" : "渠道尝试"}</small>
          <strong>
            {attempts.length}
            <span>次</span>
          </strong>
        </div>
        <div>
          <small>尝试结果</small>
          <strong className="trace-attempt-count">
            <span className="success">{successes} 成功</span>
            <span className="failure">{failures} 失败</span>
          </strong>
        </div>
      </div>
      {run.ambiguous && (
        <p className="trace-notice warning">
          同一实例重复使用了此请求
          ID，存在多个请求。各条事件单独展示，无法把它们合并为一次请求的重试链。
        </p>
      )}
      {!arrival && !run.ambiguous && (
        <p className="trace-notice">
          历史记录缺少入口时间，时间线以首条记录为起点；缺失阶段和错误原文不作推测。
        </p>
      )}
      {arrival && !final && (
        <p className="trace-notice">
          最终结果尚未记录。收到 HTTP 响应头或发送结束不代表模型处理成功。
        </p>
      )}
      <div className="trace-visual-section">
        <div className="trace-section-heading">
          <h4>
            <Activity size={16} />
            渠道耗时分布
          </h4>
          <span>
            {arrival ? "相对网关入口" : "相对首条记录"} · 点击渠道查看详情
          </span>
        </div>
        {hasTimes && attempts.length > 0 ? (
          <div
            className="trace-waterfall"
            role="region"
            aria-label="渠道请求时间线"
          >
            <div className="trace-waterfall-header">
              <span>渠道 / 尝试</span>
              <div className="trace-axis">
                {[0, 25, 50, 75, 100].map((percent) => (
                  <span key={percent} style={{ left: `${percent}%` }}>
                    {duration((span * percent) / 100)}
                  </span>
                ))}
              </div>
              <span>耗时</span>
            </div>
            {attempts.map((a, i) => (
              <AttemptWaterfall
                key={a.id}
                run={run}
                attempt={a}
                index={i}
                origin={origin}
                span={span}
                selected={selected === a.id}
                onOpen={() => open(a)}
              />
            ))}
            <div className="trace-phase-legend">
              <span className="waiting">等待响应</span>
              <span className="responding">响应已开始</span>
              <span className="text">首个正文之后</span>
              <small>
                点击节点查看具体时间 · 临近节点合并显示 · 重叠时间条表示并发
              </small>
            </div>
            <p className="trace-timing-missing">
              未采集阶段不作推测。上游末块可能包含多条 SSE；最后一条 SSE
              的独立时间未记录。
            </p>
            {downstream.length > 0 && (
              <div className="trace-downstream-timing">
                <h5>网关 → 下游</h5>
                <TimingMilestones milestones={downstream} origin={origin} />
                <p className="trace-timing-missing">
                  响应发送结束是网关侧时间，不代表客户端已接收完成。
                </p>
              </div>
            )}
          </div>
        ) : (
          <p className="trace-notice">
            {run.ambiguous
              ? "请求 ID 存在歧义，无法绘制单次请求的耗时分布。"
              : "没有可绘制的渠道尝试记录。"}
          </p>
        )}
      </div>
      <div className="trace-journey-section">
        <div className="trace-section-heading">
          <h4>
            <Route size={16} />
            请求过程
          </h4>
          <span>每次尝试合并显示派发、结果与错误</span>
        </div>
        <ol className="trace-journey">
          <li className="trace-bookend">
            <span className="trace-journey-dot" />
            <div>
              <strong>{arrival ? "请求进入网关" : "首条可见记录"}</strong>
              <small>{trace.events.length ? clock(origin) : "未记录"}</small>
            </div>
            <span>+0 ms</span>
          </li>
          {attempts.map((a, i) => (
            <li
              key={a.id}
              className={`trace-attempt ${a.tone} ${selected === a.id ? "selected" : ""}`}
            >
              <span className={`trace-journey-dot ${a.tone}`}>{i + 1}</span>
              <article>
                <div className="trace-attempt-heading">
                  <button
                    className="trace-channel-link"
                    onClick={() => open(a)}
                  >
                    {traceChannel(run, a.provider).name}
                    <ArrowUpRight size={14} />
                  </button>
                  <Status tone={a.tone} label={a.label} status={a.status} />
                </div>
                <div className="trace-attempt-subtitle">
                  <span>
                    {traceChannel(run, a.provider).group_name ||
                      `第 ${i + 1} 次渠道尝试`}
                  </span>
                  <span>
                    {a.hasStart ? "开始" : "记录"} +
                    {duration(Math.max(0, a.started - origin))}
                  </span>
                  <span>耗时 {duration(a.duration)}</span>
                </div>
                {!!a.errors.length && (
                  <div className="trace-error-message">
                    {a.errors.map((error) => (
                      <p key={error}>{error}</p>
                    ))}
                  </div>
                )}
                <details className="trace-event-disclosure">
                  <summary>展开 {a.events.length} 条阶段记录</summary>
                  <StageList events={a.events} origin={origin} />
                </details>
              </article>
            </li>
          ))}
          <li className={`trace-bookend trace-final ${finalState.tone}`}>
            <span className={`trace-journey-dot ${finalState.tone}`}>
              {finalState.tone === "success" ? (
                <Check size={14} />
              ) : finalState.tone === "failure" ? (
                <X size={14} />
              ) : null}
            </span>
            <div>
              <strong>
                {final ? `请求最终${finalState.label}` : "最终结果未记录"}
              </strong>
              {final && eventErrors(final) && (
                <small>{eventErrors(final)}</small>
              )}
            </div>
            {final && (
              <span>+{duration(Math.max(0, final.at_ms - origin))}</span>
            )}
          </li>
        </ol>
        {!!trace.gateway.length && (
          <details className="trace-gateway-records">
            <summary>网关阶段记录 · {trace.gateway.length} 条</summary>
            <StageList events={trace.gateway} origin={origin} />
          </details>
        )}
      </div>
      <details className="trace-technical trace-run-technical">
        <summary>请求标识与记录说明</summary>
        <dl className="request-trace-details">
          <dt>Request ID</dt>
          <dd>{run.request_id}</dd>
          <dt>实例</dt>
          <dd>{run.instance_id || "未记录"}</dd>
          <dt>最近事件</dt>
          <dd>
            {trace.events.length ? clock(trace.events.at(-1)!.at_ms) : "未记录"}
          </dd>
        </dl>
        <p>
          渠道名称与倍率来自当前关联。阶段详情中的渠道耗时从各自请求开始计时；响应发送结束不证明客户端已接收全部字节。
        </p>
      </details>
      {chosen && (
        <AttemptDrawer
          key={chosen.id}
          run={run}
          attempt={chosen}
          number={attempts.indexOf(chosen) + 1}
          origin={origin}
          onClose={() => setOpenAttempt(null)}
        />
      )}
    </section>
  );
}
