import { useDeferredValue, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Clock3,
  Filter,
  Globe2,
  KeyRound,
  Layers3,
  List,
  Radio,
  RefreshCw,
  Search,
  Server,
  SlidersHorizontal,
  Wallet,
  X,
} from "lucide-react";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, Metrics } from "./types";
import { ranges } from "./analytics";
import { count, ms } from "./format";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";
import type { Filters } from "./preferences";
import { endpointChoices, readKeys } from "./requestFilters";
import { useKeyRequestStats } from "./keyRequestStats";
import { friendlyReason } from "./requestTraceModel";
import { RequestTraceDialog } from "./RequestTrace";
import { Spinner } from "./ui";
import "./requestLogs.css";

export interface RequestLog {
  event_id: string;
  source_id: string;
  instance_id: string;
  request_id: string;
  trace_id: string;
  key_id: string;
  model: string;
  endpoint: string;
  stream: boolean;
  at_ms: number;
  started_ms: number | null;
  status: number;
  outcome: string;
  result: string;
  failure_reason: string;
  duration_ms: number | null;
  dispatch_ms: number | null;
  response_created_ms: number | null;
  first_text_ms: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  cache_read_tokens: number | null;
}
interface LogResult {
  data: RequestLog[];
  next_cursor: string;
  models: string[];
  keys?: { source_id: string; key_id: string }[];
  endpoints?: string[];
  from: number;
  to: number;
  import?: Metrics["import"];
}
const states: Record<string, { label: string; tone: string }> = {
  success: { label: "成功", tone: "success" },
  failed: { label: "失败", tone: "failure" },
  cancelled: { label: "已取消", tone: "warning" },
  unknown: { label: "结果未知", tone: "neutral" },
};
export function requestLogParams(filters: Filters, cursor: string) {
  const params = new URLSearchParams({
    range: filters.window,
    model: filters.model,
    endpoint: filters.endpoint,
    stream: filters.stream,
    search: filters.search.trim(),
    status: filters.statusFilter,
    balance: filters.balanceFilter,
    sort: filters.sort === "config" ? "latest" : filters.sort,
    cursor,
  });
  const split = filters.keyId.indexOf("::");
  const keySource = split < 0 ? "" : filters.keyId.slice(0, split);
  const key = split < 0 ? filters.keyId : filters.keyId.slice(split + 2);
  if (
    (keySource && filters.sourceId && keySource !== filters.sourceId) ||
    (split >= 0 && (!keySource || !key))
  )
    throw new Error("所选 API key 不属于当前来源，请重新选择。");
  if (keySource || filters.sourceId)
    params.set("source_id", keySource || filters.sourceId);
  if (key) params.set("key_id", key);
  return params;
}
function Select({
  label,
  value,
  onChange,
  icon,
  children,
  className = "",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  icon: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={`select-field ${className}`}>
      {icon}
      <select
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        {children}
      </select>
      <ChevronDown size={13} />
    </label>
  );
}
export function RequestLogsPage({
  connection: base,
  sources,
}: {
  connection: Connection;
  sources: { id: string; name: string }[];
}) {
  const storage = `${base.base}:${base.session}`;
  const [filters, setFilters] = useState(() =>
    loadFilters(storage, "requests"),
  );
  const [cursors, setCursors] = useState<string[]>([""]);
  const [page, setPage] = useState(0);
  const [revision, setRevision] = useState(0);
  const [selected, setSelected] = useState<{
    id: string;
    source: string;
    instance?: string;
  } | null>(null);
  const [auto, setAuto] = useState(false);
  const deferred = useDeferredValue(filters.search);
  const connection = useMemo(
    () => ({
      ...base,
      sourceId: filters.sourceId || undefined,
      session: `${base.session}:${filters.sourceId || "all"}`,
    }),
    [base, filters.sourceId],
  );
  useEffect(
    () => saveFilters(storage, filters, "requests"),
    [storage, filters],
  );
  function change(field: keyof Filters, value: string) {
    setFilters((previous) => ({
      ...previous,
      [field]: value,
      ...(field === "sourceId" ? { keyId: "" } : {}),
    }));
    setPage(0);
    setCursors([""]);
  }
  const keys = useQuery({
    queryKey: ["keys", connection.session],
    queryFn: ({ signal }) => readKeys(connection, signal),
    retry: false,
  });
  const stats = useKeyRequestStats(
    connection,
    {
      range: filters.window,
      model: filters.model,
      endpoint: filters.endpoint,
      stream: filters.stream,
    },
    true,
    auto,
  );
  const query = useQuery({
    queryKey: [
      "request-logs",
      base.session,
      filters.window,
      filters.sourceId,
      filters.keyId,
      filters.model,
      filters.endpoint,
      filters.stream,
      filters.statusFilter,
      filters.balanceFilter,
      filters.sort,
      deferred,
      cursors[page],
      revision,
    ],
    queryFn: ({ signal }) =>
      analyticsRequest<LogResult>(
        base,
        "/analytics/v1/request-logs?" +
          requestLogParams({ ...filters, search: deferred }, cursors[page]),
        signal,
      ),
    retry: false,
    refetchInterval: (q) =>
      initializationRetryInterval(q) || (auto && page === 0 ? 30_000 : false),
    refetchIntervalInBackground: false,
  });
  const data = query.data;
  const models = [
    ...new Set([
      ...(data?.models || []),
      ...(filters.model ? [filters.model] : []),
    ]),
  ].sort();
  const endpoints = [
    ...new Set([
      ...endpointChoices,
      ...(data?.endpoints ||
        data?.data.map((row) => row.endpoint).filter(Boolean) ||
        []),
      ...(filters.endpoint !== "all" ? [filters.endpoint] : []),
    ]),
  ];
  const knownKeys = keys.data?.data || [];
  const historyKeys = (data?.keys || [])
    .filter(
      (key) =>
        !knownKeys.some(
          (known) =>
            known.key_id ===
            (base.account ? `${key.source_id}::${key.key_id}` : key.key_id),
        ),
    )
    .map((key) => ({
      key_id: base.account ? `${key.source_id}::${key.key_id}` : key.key_id,
      source_id: key.source_id,
      source_name:
        sources.find((s) => s.id === key.source_id)?.name || key.source_id,
      position: 0,
      prefix: `历史 key · ${key.key_id.slice(0, 10)}…`,
    }));
  const keyList = [...knownKeys, ...historyKeys];
  const keyLabels = new Map(
    keyList.map((key) => [
      key.key_id,
      `${key.source_name ? key.source_name + " · " : ""}${key.position ? `Key ${key.position} · ` : ""}${key.prefix}`,
    ]),
  );
  const sourceName = (id: string) =>
    sources.find((s) => s.id === id)?.name || id || "当前来源";
  const rowKey = (row: RequestLog) =>
    base.account ? `${row.source_id}::${row.key_id}` : row.key_id;
  const hasFilters = Object.keys(defaultFilters).some(
    (k) => filters[k as keyof Filters] !== defaultFilters[k as keyof Filters],
  );
  function refresh() {
    setPage(0);
    setCursors([""]);
    setRevision((v) => v + 1);
    void stats.refetch();
  }
  function open(row: RequestLog) {
    if (row.request_id || row.trace_id)
      setSelected({
        id: row.request_id || row.trace_id,
        source: row.source_id,
        instance: row.instance_id,
      });
  }
  const traceSource =
    filters.sourceId ||
    (filters.keyId.includes("::") ? filters.keyId.split("::")[0] : "");
  return (
    <section className="data-panel request-logs-panel">
      <div className="data-heading">
        <div className="data-title">
          <List size={19} />
          <h2>请求日志</h2>
        </div>
        <div className="data-actions">
          <form
            className="search-field"
            onSubmit={(e) => {
              e.preventDefault();
              setPage(0);
              setCursors([""]);
              setRevision((v) => v + 1);
            }}
          >
            <Search size={17} />
            <input
              aria-label="搜索请求"
              placeholder="搜索 Request ID / Trace ID…"
              value={filters.search}
              maxLength={512}
              onChange={(e) => change("search", e.target.value)}
            />
            {!!filters.search && (
              <button
                type="button"
                className="icon-button"
                aria-label="清除搜索"
                onClick={() => change("search", "")}
              >
                <X size={14} />
              </button>
            )}
          </form>
          <button
            className="button small"
            disabled={query.isFetching}
            onClick={refresh}
          >
            {query.isFetching ? <Spinner small /> : <RefreshCw size={15} />}
            刷新日志
          </button>
          <label className="auto-refresh">
            <input
              type="checkbox"
              checked={auto}
              onChange={(e) => setAuto(e.target.checked)}
            />
            <span />
            自动刷新
          </label>
        </div>
      </div>
      <div className="filters" aria-label="日志筛选">
        <Select
          label="时间范围筛选"
          className="time-select"
          icon={<Clock3 size={15} />}
          value={filters.window}
          onChange={(v) => change("window", v)}
        >
          {ranges.map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </Select>
        <Select
          label="uni-api 来源"
          className="source-select"
          icon={<Server size={15} />}
          value={filters.sourceId}
          onChange={(v) => change("sourceId", v)}
        >
          <option value="">全部来源</option>
          {sources.map((s) => (
            <option value={s.id} key={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        <Select
          label="API key 筛选"
          className="key-select"
          icon={<KeyRound size={15} />}
          value={filters.keyId}
          onChange={(v) => change("keyId", v)}
        >
          <option value="">全部 API key · {stats.label()}</option>
          {filters.keyId && !keyLabels.has(filters.keyId) && (
            <option value={filters.keyId}>
              历史 API key · {filters.keyId}
            </option>
          )}
          {keyList.map((key) => (
            <option value={key.key_id} key={key.key_id}>
              {keyLabels.get(key.key_id)} · {stats.label(key)}
            </option>
          ))}
        </Select>
        <Select
          label="模型筛选"
          className="model-select"
          icon={<Layers3 size={15} />}
          value={filters.model}
          onChange={(v) => change("model", v)}
        >
          <option value="">全部模型</option>
          {models.map((model) => (
            <option key={model}>{model}</option>
          ))}
        </Select>
        <Select
          label="端点筛选"
          icon={<Globe2 size={13} />}
          value={filters.endpoint}
          onChange={(v) => change("endpoint", v)}
        >
          <option value="all">全部端点</option>
          {endpoints.map((path) => (
            <option key={path}>{path}</option>
          ))}
        </Select>
        <Select
          label="流式状态筛选"
          icon={<Radio size={13} />}
          value={filters.stream}
          onChange={(v) => change("stream", v)}
        >
          <option value="all">全部流式状态</option>
          <option value="true">流式</option>
          <option value="false">非流式</option>
        </Select>
        <button
          className={`filter-chip ${filters.balanceFilter ? "active" : ""}`}
          aria-pressed={!!filters.balanceFilter}
          onClick={() =>
            change("balanceFilter", filters.balanceFilter ? "" : "low")
          }
        >
          <Wallet size={13} />
          余额不足{filters.balanceFilter && <X size={12} />}
        </button>
        <Select
          label="请求状态筛选"
          icon={<Filter size={13} />}
          value={filters.statusFilter}
          onChange={(v) => change("statusFilter", v)}
        >
          <option value="">全部状态</option>
          {Object.entries(states).map(([value, state]) => (
            <option value={value} key={value}>
              {state.label}
            </option>
          ))}
        </Select>
        <Select
          label="排序"
          icon={<SlidersHorizontal size={13} />}
          value={filters.sort}
          onChange={(v) => change("sort", v)}
        >
          <option value="config">最新请求优先</option>
          <option value="oldest">最早请求优先</option>
          <option value="success">成功请求优先</option>
          <option value="latency">首字延迟从低到高</option>
          <option value="wait">请求前等待从低到高</option>
        </Select>
        <button
          className="reset-filters"
          disabled={!hasFilters}
          onClick={() => {
            setFilters({ ...defaultFilters });
            setPage(0);
            setCursors([""]);
          }}
        >
          <X size={13} />
          重置筛选
        </button>
      </div>
      <p className="request-log-hint">
        每行是一条已完成请求，状态为全部重试后的最终结果。点击查看完整时间线。
        {data && (
          <>
            {" "}
            更新于{" "}
            {new Date(data.to).toLocaleTimeString("zh-CN", { hour12: false })}
          </>
        )}
      </p>
      {!!filters.search.trim() && (
        <div className="request-log-search-help">
          <span>在当前筛选范围内搜索请求 ID / Trace ID。</span>
          <button
            className="button small ghost"
            onClick={() =>
              setSelected({ id: filters.search.trim(), source: traceSource })
            }
          >
            按此 ID 查看完整追踪（不限时间）
          </button>
        </div>
      )}
      {keys.error && (
        <p className="coverage-note">
          API key 目录暂不可用；请求日志仍可浏览。
        </p>
      )}
      {data?.import?.caught_up === false && (
        <p className="coverage-note">
          请求记录仍在同步，当前日志可能不完整。刷新可读取最新已同步记录。
        </p>
      )}
      {query.error && (
        <p role="alert" className="error-banner">
          {query.error.message}
        </p>
      )}
      {query.isPending ? (
        <div className="request-log-empty" role="status">
          <Spinner />
          正在读取请求日志…
        </div>
      ) : !data?.data.length ? (
        <div className="request-log-empty">
          <Search size={26} />
          <h3>{query.isError ? "请求日志暂不可用" : "没有匹配的请求"}</h3>
          <p>可调整时间范围或筛选条件后刷新。</p>
        </div>
      ) : (
        <>
          <div className="request-log-table-wrap">
            <table className="request-log-table">
              <thead>
                <tr>
                  <th>时间 / 请求 ID</th>
                  <th>来源 / API key</th>
                  <th>模型 / 端点</th>
                  <th>最终结果</th>
                  <th>总耗时</th>
                  <th>首字延迟</th>
                  <th>请求前等待</th>
                  <th>输入 / 输出 token</th>
                </tr>
              </thead>
              <tbody>
                {data.data.map((row) => (
                  <tr
                    key={row.event_id}
                    className="request-log-row"
                    onClick={() => open(row)}
                  >
                    <td>
                      <time dateTime={new Date(row.at_ms).toISOString()}>
                        {new Date(row.at_ms).toLocaleString("zh-CN", {
                          hour12: false,
                        })}
                      </time>
                      <button
                        className="request-log-id"
                        disabled={!row.request_id && !row.trace_id}
                        onClick={(e) => {
                          e.stopPropagation();
                          open(row);
                        }}
                      >
                        {row.request_id || row.trace_id || "请求 ID 未记录"}
                      </button>
                    </td>
                    <td>
                      <strong>{sourceName(row.source_id)}</strong>
                      <small title={row.key_id}>
                        {keyLabels
                          .get(rowKey(row))
                          ?.replace(`${sourceName(row.source_id)} · `, "") ||
                          (row.key_id
                            ? `历史 key · ${row.key_id.slice(0, 10)}…`
                            : "未记录 API key")}
                      </small>
                    </td>
                    <td>
                      <strong className="mono">
                        {row.model || "未记录模型"}
                      </strong>
                      <small>
                        {row.endpoint || "未记录端点"} ·{" "}
                        {row.stream ? "流式" : "非流式"}
                      </small>
                    </td>
                    <td>
                      <span
                        className={`trace-status ${states[row.result]?.tone || "neutral"}`}
                      >
                        {states[row.result]?.label || "未知"}
                        {row.status > 0 && ` · HTTP ${row.status}`}
                      </span>
                      {row.failure_reason && (
                        <small
                          className="request-log-error"
                          title={friendlyReason(row.failure_reason)}
                        >
                          {friendlyReason(row.failure_reason)}
                        </small>
                      )}
                    </td>
                    <td className="mono">{ms(row.duration_ms)}</td>
                    <td className="mono">
                      {ms(row.response_created_ms ?? row.first_text_ms)}
                    </td>
                    <td className="mono">{ms(row.dispatch_ms)}</td>
                    <td className="mono">
                      {row.input_tokens == null ? "—" : count(row.input_tokens)}{" "}
                      /{" "}
                      {row.output_tokens == null
                        ? "—"
                        : count(row.output_tokens)}
                      {row.cache_read_tokens != null && (
                        <small>缓存读取 {count(row.cache_read_tokens)}</small>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <footer className="request-log-pagination">
            <span>
              第 {page + 1} 页 · 本页 {data.data.length} 条请求
            </span>
            <div>
              <button
                className="icon-button"
                aria-label="上一页"
                disabled={page === 0 || query.isFetching}
                onClick={() => setPage((p) => p - 1)}
              >
                <ChevronLeft size={16} />
              </button>
              <button
                className="icon-button"
                aria-label="下一页"
                disabled={!data.next_cursor || query.isFetching}
                onClick={() => {
                  setCursors((current) => [
                    ...current.slice(0, page + 1),
                    data.next_cursor,
                  ]);
                  setPage((p) => p + 1);
                }}
              >
                <ChevronRight size={16} />
              </button>
            </div>
          </footer>
        </>
      )}
      {selected && (
        <RequestTraceDialog
          key={`${selected.source}:${selected.instance}:${selected.id}`}
          connection={base}
          sources={sources}
          requestId={selected.id}
          sourceId={selected.source}
          instanceId={selected.instance}
          onClose={() => setSelected(null)}
        />
      )}
    </section>
  );
}
