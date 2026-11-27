import { TableLoading } from "./PageLoading";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { queryOptions, useQuery, useQueryClient } from "@tanstack/react-query";
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
} from "./icons";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, Metrics } from "./types";
import { ranges } from "./analytics";
import { count, ms } from "./format";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";
import type { Filters } from "./preferences";
import { endpointChoices, readKeys } from "./requestFilters";
import { useKeyRequestStats } from "./keyRequestStats";
import { friendlyReason } from "./requestTraceModel";
import { RequestTraceDialog, requestTraceOptions } from "./RequestTrace";
import { LatencyBadge } from "./LatencyBadge";
import { Spinner, Tip } from "./ui";
import { WorkbenchDisclosure, WorkbenchNotice } from "./Workbench";
import { TableColumns, useTableColumns } from "./TableColumns";
import { readWorkspace, requestLocation, validLogWindow, type LogPosition, type LogWindow } from "./workspaceLocation";
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
  request_type?: string;
  requested_service_tier?: string;
  stream: boolean;
  at_ms: number;
  started_ms: number | null;
  status: number;
  response_headers_status?: number | null;
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
  observed_attempts?: number | null;
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
const logColumns = [
  { id: "request", label: "时间 / 请求 ID" },
  { id: "source", label: "来源 / API Key" },
  { id: "model", label: "模型 / 端点" },
  { id: "type", label: "类型" },
  { id: "result", label: "最终结果" },
  { id: "duration", label: "总耗时" },
  { id: "latency", label: "响应创建" },
  { id: "wait", label: "请求前等待" },
  { id: "tokens", label: "输入 / 输出 Token" },
] as const;
type LogColumn = typeof logColumns[number]["id"];
const numericLogColumns: ReadonlySet<LogColumn> = new Set(["duration", "latency", "wait", "tokens"]);
const defaultLogColumns = logColumns.filter(({ id }) => id === "request" || id === "type" || id === "result" || id === "duration" || id === "latency").map(({ id }) => id);
const logColumnLabels = Object.fromEntries(logColumns.map(({ id, label }) => [id, label])) as Record<LogColumn, string>;
export function requestLogParams(filters: Filters, cursor: string, exact?: LogWindow | null) {
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
  if (exact) {
    params.set("from_ms", String(exact.from));
    params.set("to_ms", String(exact.to));
  }
  return params;
}
function Select({
  label,
  value,
  onChange,
  icon,
  children,
  className = "",
  accessory,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  icon: ReactNode;
  children: ReactNode;
  className?: string;
  accessory?: string;
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
      {accessory && <span className="log-filter-accessory" title={accessory}>{accessory.replace(/^请求成功率 (?=\d)/, "")}</span>}
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
  const queryClient = useQueryClient();
  const storage = `${base.base}:${base.session}`;
  const workspaceOwner = base.account ? JSON.stringify([base.base,base.session]) : base.base;
  const [incoming] = useState(() => readWorkspace(location.hash, !!base.account));
  const [filters, setFilters] = useState(() => {
    const saved = loadFilters(storage, "requests");
    // A normal visit starts with all statuses; scoped links can intentionally filter.
    return incoming.view === "requests" && incoming.filters
      ? incoming.filters
      : {...saved, search: "", statusFilter: defaultFilters.statusFilter};
  });
  const [cursors, setCursors] = useState<string[]>([""]);
  const [page, setPage] = useState(0);
  const [revision, setRevision] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<Error | null>(null);
  const refreshToken = useRef(0);
  const [selected, setSelected] = useState<LogPosition | null>(incoming.request);
  const [exact, setExact] = useState<LogWindow | null>(incoming.logWindow);
  const [editWindow, setEditWindow] = useState(false);
  const [customColumns, setCustomColumns] = useTableColumns("uni-console:log-columns:v2", logColumns, {key:"uni-console:log-columns:v1", append:["type"]});
  const visibleColumns = customColumns ?? defaultLogColumns;
  const hasRequestColumn = visibleColumns.includes("request");
  const [fromInput, setFromInput] = useState("");
  const [toInput, setToInput] = useState("");
  const [rangeError, setRangeError] = useState("");
  const [auto, setAuto] = useState(false);
  const [search, setSearch] = useState(filters.search.trim());
  const [composing, setComposing] = useState(false);
  const intent = useRef<number | null>(null);
  const autoActive = auto && page === 0 && !exact;
  function leavePage() {
    refreshToken.current++;
    setRefreshing(false);
    setRefreshError(null);
  }
  function resetPage() {
    leavePage();
    setPage(0);
    setCursors([""]);
  }
  function clearSearch() {
    setFilters(current => ({ ...current, search: "" }));
    setSearch("");
    resetPage();
  }
  useEffect(() => {
    if (composing) return;
    const timer = window.setTimeout(() => {
      if (search !== filters.search.trim()) {
        setSearch(filters.search.trim());
        resetPage();
      }
    }, 300);
    return () => window.clearTimeout(timer);
  }, [filters.search, search, composing]);
  const connection = useMemo(
    () => ({
      ...base,
      sourceId: filters.sourceId || undefined,
      session: `${base.session}:${filters.sourceId || "all"}`,
    }),
    [base, filters.sourceId],
  );
  useEffect(
    // An ID lookup is specific to this visit; don't store or silently reapply it.
    () => saveFilters(storage, {...filters, search: ""}, "requests"),
    [storage, filters],
  );
  function change(field: keyof Filters, value: string) {
    setFilters((previous) => ({
      ...previous,
      [field]: value,
      ...(field === "sourceId" ? { keyId: "" } : {}),
    }));
    if (field === "window") { setExact(null); setEditWindow(false); setRangeError(""); }
    if (field !== "search") resetPage();
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
    !exact,
    autoActive,
  );
  const scope = JSON.stringify([
    base.base,
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
    search,
    exact?.from,
    exact?.to,
  ]);
  useEffect(() => {
    refreshToken.current++;
    setRefreshing(false);
    setRefreshError(null);
    return () => {
      refreshToken.current++;
    };
  }, [scope]);
  const logOptions = (cursor: string, version: number) =>
    queryOptions({
      queryKey: ["request-logs", scope, cursor, version],
      queryFn: ({ signal }) =>
        analyticsRequest<LogResult>(
          base,
          "/analytics/v1/request-logs?" +
            requestLogParams({ ...filters, search }, cursor, exact),
          signal,
        ),
      retry: false,
      // The prepared refresh result is fresh when the view switches to it.
      staleTime: 1000,
    });
  const query = useQuery({
    ...logOptions(cursors[page], revision),
    refetchInterval: (q) =>
      initializationRetryInterval(q) || (autoActive ? 30_000 : false),
    refetchIntervalInBackground: false,
  });
  const busy = query.isFetching || refreshing;
  const error = refreshError || query.error;
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
  const knownKeyIds = new Set(knownKeys.map(key => key.key_id));
  const sourceById = new Map(sources.map(source => [source.id, source.name]));
  const historyKeys = (data?.keys || [])
    .filter(
      (key) =>
        !knownKeyIds.has(base.account ? `${key.source_id}::${key.key_id}` : key.key_id),
    )
    .map((key) => ({
      key_id: base.account ? `${key.source_id}::${key.key_id}` : key.key_id,
      source_id: key.source_id,
      source_name:
        sourceById.get(key.source_id) || key.source_id,
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
    sourceById.get(id) || id || "当前来源";
  const rowKey = (row: RequestLog) =>
    base.account ? `${row.source_id}::${row.key_id}` : row.key_id;
  const hasFilters = Object.keys(defaultFilters).some(
    (k) => filters[k as keyof Filters] !== defaultFilters[k as keyof Filters],
  );
  async function refresh(latest = false) {
    if (refreshing) return;
    const token = ++refreshToken.current;
    const nextRevision = revision + 1;
    setRefreshing(true);
    setRefreshError(null);
    if (!exact) void stats.refetch();
    try {
      // Keep the current page mounted until its replacement is available.
      await queryClient.fetchQuery({
        ...logOptions(latest ? "" : cursors[page], nextRevision),
        staleTime: 0,
      });
      if (token !== refreshToken.current) return;
      if (latest) resetPage();
      else setCursors(current => current.slice(0, page + 1));
      setRevision(nextRevision);
    } catch (error) {
      if (token === refreshToken.current)
        setRefreshError(
          error instanceof Error ? error : new Error("刷新请求日志失败"),
        );
    } finally {
      if (token === refreshToken.current) setRefreshing(false);
    }
  }
  function showTrace(position: LogPosition) {
    history.replaceState({...history.state, workspaceOwner}, "", requestLocation(filters, selected, exact));
    history.pushState({logDetail:true, workspaceOwner}, "", requestLocation(filters, position, exact));
    setSelected(position);
  }
  function open(row: RequestLog) {
    cancelIntent();
    if (row.request_id || row.trace_id)
      showTrace({
        id: row.request_id || row.trace_id,
        source: row.source_id,
        instance: row.instance_id,
      });
  }
  function cancelIntent() {
    if (intent.current != null) window.clearTimeout(intent.current);
    intent.current = null;
  }
  function preload(row: RequestLog) {
    cancelIntent();
    const id = row.request_id || row.trace_id;
    const net = (navigator as Navigator & {connection?: {saveData?:boolean;effectiveType?:string}}).connection;
    if (id && !net?.saveData && !net?.effectiveType?.includes("2g")) intent.current = window.setTimeout(() => {
      intent.current = null;
      void queryClient.prefetchQuery(requestTraceOptions(base, id, row.source_id, row.instance_id));
    }, 260);
  }
  useEffect(() => cancelIntent, [scope, page]);
  useEffect(() => {
    const restore = () => {
      const next = readWorkspace(location.hash, !!base.account);
      if (next.view === "requests") {
        setSelected(next.request);
        if (next.filters) setFilters(current => {
          const scoped = {...next.filters!, search:current.search};
          return JSON.stringify(scoped) === JSON.stringify(current) ? current : scoped;
        });
        setExact(current => current?.from === next.logWindow?.from && current?.to === next.logWindow?.to ? current : next.logWindow);
      }
    };
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, [base.account]);
  useEffect(() => {
    if (readWorkspace(location.hash, !!base.account).view === "requests")
      history.replaceState({...history.state, workspaceOwner}, "", requestLocation(filters, selected, exact));
  }, [filters, exact, selected, base.account, workspaceOwner]);
  function applyWindow(event: React.FormEvent) {
    event.preventDefault();
    const from = Date.parse(fromInput), to = Date.parse(toInput);
    if (!validLogWindow(from,to)) {
      setRangeError("请选择有效起止时间：开始早于结束，最长 366 天，结束不能晚于当前时间。");
      return;
    }
    setExact({from,to});
    setEditWindow(false);
    setRangeError("");
    resetPage();
  }
  const traceSource =
    filters.sourceId ||
    (filters.keyId.includes("::") ? filters.keyId.split("::")[0] : "");
  const traceButton = (row: RequestLog, compact = false) => {
    const id = row.request_id || row.trace_id;
    return <button type="button" className="request-log-id" title={id}
      aria-label={id ? (compact ? `查看请求 ${id}` : id) : undefined}
      disabled={!id} onClick={event => { event.stopPropagation(); open(row); }}>
      {compact ? "查看" : !id ? "—" : <>
        <span className="log-id-wide">{id}</span>
        <span className="log-id-narrow" aria-hidden="true">{id.length > 10 ? `${id.slice(0, 4)}…${id.slice(-5)}` : id}</span>
      </>}
    </button>;
  };
  function renderCell(row: RequestLog, id: LogColumn) {
    switch (id) {
      case "request": {
        const at = new Date(row.at_ms);
        return <td key={id} className="log-identity">
        <time dateTime={at.toISOString()}><span>{at.toLocaleDateString("zh-CN")}</span>{" "}<span>{at.toLocaleTimeString("zh-CN", { hour12: false })}</span></time>
        {traceButton(row)}
      </td>;
      }
      case "source": return <td key={id} className="log-extra-column">
        <strong title={sourceName(row.source_id)}>{sourceName(row.source_id)}</strong>
        <small title={row.key_id}>{keyLabels.get(rowKey(row))?.replace(`${sourceName(row.source_id)} · `, "") ||
          (row.key_id ? `历史 key · ${row.key_id.slice(0, 10)}…` : "未记录 API key")}</small>
      </td>;
      case "model": return <td key={id} className="log-extra-column">
        <strong className="mono" title={row.model}>{row.model || "未记录模型"}</strong>
        <small title={row.endpoint}>{row.endpoint || "未记录端点"} · {row.stream ? "流式" : "非流式"}</small>
      </td>;
      case "type": {
        const compact = row.request_type === "compaction" || row.endpoint.replace(/\/+$/, "") === "/v1/responses/compact";
        const tier = { fast:"Fast", priority:"Fast", ultrafast:"Ultrafast", flex:"Flex", scale:"Scale", auto:"Auto" }[row.requested_service_tier || ""];
        return <td key={id} className="log-request-type" title="请求类型和请求档位，不代表实际计费档位">
          {compact && <span className="log-type-tag">压缩</span>}
          {tier && <span className="log-type-tag">{tier}</span>}
          {!compact && !tier && (row.request_type === "standard" && row.requested_service_tier === "default" ? <span>普通</span> : <span className="muted">未记录</span>)}
        </td>;
      }
      case "result": {
        const interrupted = row.stream && row.result === "failed" && row.status === 502 &&
          row.response_headers_status === 200 &&
          ["transport_error", "missing_response_completed"].includes(row.failure_reason);
        const state = states[row.result];
        return <td key={id} className="log-outcome">
          <Tip text={<div className="table-diagnostic-tip">
            <div>{row.observed_attempts != null ? `已记录 ${row.observed_attempts} 次尝试` : "尝试数未确认"}</div>
            {interrupted && <div>网关最终判定：{row.status}</div>}
            {row.failure_reason && <div>{friendlyReason(row.failure_reason)}</div>}
          </div>}>
            <span className={`trace-status ${state?.tone || "neutral"}`} data-interrupted={interrupted || undefined}>
              {interrupted ? "响应头 HTTP 200 · 流中断" : `${state?.label || "未知"}${row.status > 0 ? ` · HTTP ${row.status}` : ""}`}
            </span>
          </Tip>
        </td>;
      }
      case "duration": return <td key={id} className="mono" data-align="end">{ms(row.duration_ms)}</td>;
      case "latency": return <td key={id} className="mono" data-align="end">
        <LatencyBadge value={row.response_created_ms ?? row.first_text_ms} />
        {row.response_created_ms == null && row.first_text_ms != null && <small>首个文本</small>}
      </td>;
      case "wait": return <td key={id} className="mono log-extra-column" data-align="end">{ms(row.dispatch_ms)}</td>;
      case "tokens": return <td key={id} className="mono log-extra-column" data-align="end">
        {row.input_tokens == null ? "—" : count(row.input_tokens)} / {row.output_tokens == null ? "—" : count(row.output_tokens)}
        {row.cache_read_tokens != null && <small>缓存读取 {count(row.cache_read_tokens)}</small>}
      </td>;
    }
  }
  return (
    <section className="data-panel request-logs-panel" data-tools="expanded" data-columns={customColumns ? "custom" : "summary"}>
      <div className="data-heading workbench-heading">
        <div className="data-title">
          <List size={19} />
          <h2>请求日志</h2>
          <WorkbenchNotice messages={[
            keys.error && "API key 目录暂不可用；请求日志仍可浏览。",
            data?.import?.caught_up === false && "请求记录仍在同步，当前日志可能不完整。",
          ]} />
          <WorkbenchNotice urgent label="请求日志读取失败" messages={[error && `${refreshError && data ? "刷新失败，保留当前日志。" : ""}${error.message}`]} />
        </div>
        <div className="workbench-tools">
          <WorkbenchDisclosure label={search ? "定位 ID · 已用" : "定位 ID"} icon={<Search size={14} aria-hidden="true" />}>
          <form
            className="search-field"
            onSubmit={(e) => {
              e.preventDefault();
              if (!composing && search !== filters.search.trim()) {
                setSearch(filters.search.trim());
                resetPage();
              }
            }}
          >
            <Search size={17} />
            <input
              aria-label="搜索请求"
              placeholder="搜索 Request ID / Trace ID…"
              value={filters.search}
              maxLength={512}
              onChange={(e) => change("search", e.target.value)}
              onCompositionStart={() => setComposing(true)}
              onCompositionEnd={() => setComposing(false)}
            />
            {!!filters.search && (
              <button
                type="button"
                className="icon-button"
                aria-label="清除搜索"
                onClick={clearSearch}
              >
                <X size={14} />
              </button>
            )}
          </form>
          {!!filters.search.trim() && <button type="button" className="button small" onClick={() =>
            showTrace({ id: filters.search.trim(), source: traceSource })}>按此 ID 查看完整追踪（不限时间）</button>}
          </WorkbenchDisclosure>
          <WorkbenchDisclosure label={hasFilters || exact ? "筛选 · 已用" : "筛选"} icon={<Filter size={14} aria-hidden="true" />}>
      <div id="request-log-filters" className="filters request-log-filters" aria-label="日志筛选">
          <button
            className="button small"
            aria-label="刷新日志"
            aria-busy={busy}
            disabled={busy}
            onClick={() => void refresh()}
          >
            {busy ? <Spinner small /> : <RefreshCw size={15} />}
            刷新日志
          </button>
          {page > 0 && <button className="button small" disabled={busy} onClick={() => void refresh(true)}>{exact ? "返回第 1 页" : "查看最新"}</button>}
          <label className="auto-refresh" title={auto && !autoActive ? (exact ? "固定时间范围暂停自动刷新" : "查看历史页时暂停自动刷新") : undefined}>
            <input
              type="checkbox"
              checked={autoActive}
              disabled={page > 0 || !!exact}
              onChange={(e) => setAuto(e.target.checked)}
            />
            <span />
            自动刷新
            {auto && !autoActive && <small className="sr-only" role="status">已暂停</small>}
          </label>
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
        <button className="button small log-time-button" aria-expanded={editWindow}
          title={exact ? `${new Date(exact.from).toLocaleString()} – ${new Date(exact.to).toLocaleString()}` : undefined}
          onClick={() => {
            if (!editWindow && exact) {
              const localTime = (value: number) => new Date(value - new Date(value).getTimezoneOffset() * 60_000).toISOString().slice(0, 23);
              setFromInput(localTime(exact.from));
              setToInput(localTime(exact.to));
            }
            setEditWindow(!editWindow);
          }}>{exact ? "修改固定时间" : "指定起止时间"}</button>
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
          accessory={filters.keyId && !exact ? stats.label(keyList.find(key => key.key_id === filters.keyId) || {key_id:filters.keyId}) : undefined}
        >
          <option value="">全部 API Key</option>
          {filters.keyId && !keyLabels.has(filters.keyId) && (
            <option value={filters.keyId}>
              历史 API key · {filters.keyId}
            </option>
          )}
          {keyList.map((key) => (
            <option value={key.key_id} key={key.key_id}>
              {keyLabels.get(key.key_id)}
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
          <optgroup label="请求总耗时">
            <option value="duration">总耗时从低到高</option>
            <option value="duration-desc">总耗时从高到低</option>
          </optgroup>
          <optgroup label="首字事件">
            <option value="created">响应创建从低到高</option>
            <option value="created-desc">响应创建从高到低</option>
            <option value="text">首个文本从低到高</option>
            <option value="text-desc">首个文本从高到低</option>
          </optgroup>
          <option value="latency">首字延迟从低到高（创建或首个文本）</option>
          <option value="latency-desc">首字延迟从高到低（创建或首个文本）</option>
          <option value="wait">请求前等待从低到高</option>
          <option value="wait-desc">请求前等待从高到低</option>
        </Select>
        <button
          className="button small ghost reset-filters"
          disabled={!hasFilters && !exact}
          onClick={() => {
            setFilters({ ...defaultFilters });
            setSearch("");
            setExact(null);
            setEditWindow(false);
            setRangeError("");
            resetPage();
          }}
        >
          <X size={13} />
          重置筛选
        </button>
      </div>
      <TableColumns choices={logColumns} value={visibleColumns} onChange={setCustomColumns} onReset={() => setCustomColumns(null)} />
      {editWindow && <form className="request-log-window" onSubmit={applyWindow}>
        <label>开始时间（本机） <input type="datetime-local" step="any" required value={fromInput} onChange={event=>setFromInput(event.target.value)} /></label>
        <label>结束时间（本机） <input type="datetime-local" step="any" required value={toInput} onChange={event=>setToInput(event.target.value)} /></label>
        <button className="button small" type="submit">应用固定时间</button>
        {rangeError && <p role="alert">{rangeError}</p>}
      </form>}
          </WorkbenchDisclosure>
        </div>
      </div>
      {query.isPending ? (
        <TableLoading kind="requests" />
      ) : !data?.data.length ? (
        <div className="request-log-empty">
          <Search size={26} />
          <h3>{query.isError ? "请求日志暂不可用" : "没有匹配的请求"}</h3>
          <p>{query.isError ? "日志未加载，请重试。" : search ? "当前 ID 搜索没有匹配记录。" : "可调整时间范围或筛选条件。关联日志尚在同步时，稍后再刷新。"}</p>
          {(query.isError || search) && <div className="request-log-empty-actions">
            {query.isError && <button className="button small" type="button" disabled={busy} aria-busy={busy} onClick={() => void refresh()}>
              {busy ? <Spinner small /> : <RefreshCw size={15} />}重试加载
            </button>}
            {search && <button className="button small" type="button" onClick={clearSearch}>清除 ID 搜索</button>}
          </div>}
        </div>
      ) : (
        <>
          <div className="request-log-table-wrap" role="region" aria-label="请求日志表" tabIndex={0}>
            <table className="request-log-table" style={customColumns ? {minWidth: `max(100%, ${visibleColumns.length * 118 + (hasRequestColumn ? 64 : 58)}px)`} : undefined}>
              <thead>
                <tr>
                  {visibleColumns.map(id => <th scope="col" key={id} data-align={numericLogColumns.has(id) ? "end" : undefined} className={id === "request" ? "log-identity" : id === "result" ? "log-outcome" : id === "type" ? "log-request-type" : undefined}>{logColumnLabels[id]}</th>)}
                  {!hasRequestColumn && <th className="log-open">详情</th>}
                </tr>
              </thead>
              <tbody>
                {data.data.map((row) => (
                  <tr
                    key={row.event_id}
                    className="request-log-row"
                    onClick={() => open(row)}
                    onPointerEnter={() => preload(row)}
                    onPointerLeave={cancelIntent}
                    onFocus={() => preload(row)}
                    onBlur={cancelIntent}
                  >
                    {visibleColumns.map(id => renderCell(row, id))}
                    {!hasRequestColumn && <td className="log-open">{traceButton(row, true)}</td>}
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
                disabled={page === 0}
                onClick={() => { leavePage(); setPage((p) => p - 1); }}
              >
                <ChevronLeft size={16} />
              </button>
              <button
                className="icon-button"
                aria-label="下一页"
                disabled={!data.next_cursor}
                onClick={() => {
                  leavePage();
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
          onClose={() => {
            if (history.state?.logDetail) history.back();
            else setSelected(null);
          }}
        />
      )}
    </section>
  );
}
