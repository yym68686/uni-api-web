import {
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { FormEvent, ReactNode } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, m as motion } from "motion/react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  Activity,
  ArrowRight,
  ArrowUpRight,
  Check,
  CheckCheck,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Database,
  Clock3,
  Command,
  Eye,
  EyeOff,
  Filter,
  Gauge,
  Globe2,
  KeyRound,
  Layers3,
  Link2,
  LogOut,
  Radio,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  TriangleAlert,
  Unplug,
  Wallet,
  X,
} from "lucide-react";
import {
  ApiError,
  AnalyticsInitializingError,
  initializationRetryInterval,
  channelParams,
  makeLimiter,
  request,
  analyticsRequest,
  controlRequest,
  cleanBase,
} from "./api";
import {
  CheckActions,
  ChannelCheckAction,
  LatestChannelCheck,
  checkTargets,
  useChannelChecks,
  CheckVerdict,
  withSubQuality,
  useSubQualitySummary,
} from "./ChannelChecks";
import {
  useChannelControls,
  ChannelControlCell,
  ChannelControlActions,
} from "./ChannelControls";
import { SourceSettings } from "./SourceSettings";
import { ResponseLatency } from "./LatencyBadge";
import { useSubImports, boundGroups } from "./sub2apiImports";
import type { SubImportsQuery } from "./sub2apiImports";
import { ChannelModels } from "./ChannelModels";

import { SiteLink, useChannelSites, dashboardURL } from "./ChannelSite";
import { ChannelAccess } from "./ChannelAccess";
import { CacheTrend } from "./CacheTrend";
import type { CacheTrendProps } from "./CacheTrend";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import type { ChannelCheck } from "./ChannelChecks";
import { channelName } from "./format";
import { Sub2apiChecks } from "./Sub2apiChecks";
import type { ConsoleSource } from "./SourceSettings";
import {
  defaultFilters,
  loadFilters,
  saveFilters,
  loadView,
  saveView,
} from "./preferences";
import { loadConnection, saveConnection, clearConnection } from "./session";
import type { Filters, View } from "./preferences";
import {
  balanceIsLow,
  count,
  ms,
  rate,
  rowId,
  providerId,
  summarize,
  time,
} from "./format";
import type {
  Balance,
  Catalog,
  Channel,
  Connection,
  KeyInfo,
  Metrics,
  ModelPrice,
} from "./types";
import { Brand, Empty, Spinner, Tip } from "./ui";
import { PriceSettings } from "./PriceSettings";
import { catalogMetrics, ranges, staleHistorySources } from "./analytics";
import { readMetrics } from "./metricsApi";
import {
  BalanceValue,
  Status,
  ChannelMetricHeaders,
  ChannelMetricCells,
} from "./ChannelMetrics";
import { actualCostRange } from "./actualCost";
import { useSubChannelSpend } from "./SubChannelSpend";
import { useChannelAccountBalances } from "./ChannelAccountBalances";
import { ConsoleHeader, ConsoleNavigation } from "./ConsoleChrome";
import { StartupScreen } from "./StartupScreen";
import { useTheme } from "./theme";
import { Automation } from "./Automation";

type Keys = {
  data: KeyInfo[];
  snapshot_revision: string;
  can_inspect_all: boolean;
};

function Overview({
  metrics,
  rows,
  live,
  refreshAction,
}: {
  metrics?: Metrics;
  rows: Channel[];
  live?: Map<string, number | null | undefined>;
  refreshAction: ReactNode;
}) {
  const total = metrics?.total as Record<string, any> | undefined;
  const models = (metrics as any)?.models || [];
  const currentConcurrency = live
    ? [...live.values()].reduce<number>((sum, value) => sum + (value || 0), 0)
    : null;
  const cacheRate =
    total?.cache_rate ??
    (total && total.input_tokens > 0
      ? (total.cache_read_tokens || 0) / total.input_tokens
      : null);
  return (
    <motion.section {...reveal} className="overview-grid">
      <MetricCard
        label="请求数量"
        value={total ? count(total.requests || 0) : "—"}
        sub="所选时间范围"
        icon={<Activity size={17} />}
      />
      <MetricCard
        label="渠道尝试"
        value={total ? count(total.attempts || 0) : "—"}
        sub="包含重试与失败尝试"
        icon={<Radio size={17} />}
        accent
      />
      <MetricCard
        label="Token 数量"
        value={total ? count((total.input_tokens || 0) + (total.output_tokens || 0)) : "—"}
        sub="输入 + 输出"
        icon={<Layers3 size={17} />}
      />
      <MetricCard
        label="估算消费"
        value={
          total?.estimated_cost_usd == null
            ? "—"
            : `$${Number(total.estimated_cost_usd).toFixed(4)}`
        }
        sub="依据当前模型价格"
        icon={<Wallet size={17} />}
      />
      <MetricCard
        label="渠道总并发"
        value={currentConcurrency == null ? "—" : count(currentConcurrency)}
        sub="所列渠道的全部 API key · 实时尝试"
        icon={<Gauge size={17} />}
      />
      <MetricCard
        label="缓存率"
        value={rate(cacheRate)}
        sub="缓存读取 / 输入 token"
        icon={<Database size={17} />}
      />
      <div className="data-panel overview-panel">
        <div className="data-heading overview-heading">
          <div className="data-title">
            <Gauge size={18} />
            <h2>模型消费</h2>
          </div>
          {refreshAction}
        </div>
        <div className="overview-list">
          {models.length ? (
            models.map((item: any) => (
              <div className="overview-row" key={item.model}>
                <strong>{item.model}</strong>
                <span>
                  {count((item.input_tokens || 0) + (item.output_tokens || 0))}{" "}
                  tokens
                </span>
                <b>
                  {item.estimated_cost_usd == null
                    ? "—"
                    : `$${Number(item.estimated_cost_usd).toFixed(4)}`}
                </b>
              </div>
            ))
          ) : (
            <Empty title="暂无模型事实" icon={<Activity size={22} />}>
              等待 S3 事实导入。
            </Empty>
          )}
        </div>
      </div>
      <div className="data-panel overview-panel">
        <div className="data-title">
          <Radio size={18} />
          <h2>当前渠道</h2>
        </div>
        <div className="overview-list">
          {rows.slice(0, 12).map((row) => (
            <div className="overview-row" key={rowId(row)}>
              <strong>{channelName(row)}</strong>
              {row.source_name && (
                <small className="source-label">{row.source_name}</small>
              )}
              <span>{row.model}</span>
              <b>
                {live?.get(rowId(row)) == null
                  ? "—"
                  : `${live.get(rowId(row))} 并发`}
              </b>
            </div>
          ))}
        </div>
      </div>
    </motion.section>
  );
}

const endpointChoices = [
  "/v1/responses",
  "/v1/responses/compact",
  "/v1/chat/completions",
  "/v1/messages",
  "/v1/embeddings",
  "/v1/images/generations",
  "/v1/images/edits",
  "/v1/audio/speech",
  "/v1/audio/transcriptions",
  "/v1/audio/translations",
  "/v1/moderations",
];

async function readKeys(connection: Connection, signal: AbortSignal) {
  const keys = await request<Keys>(connection, "/v1/api-keys", signal);
  if (!Array.isArray(keys.data))
    throw new Error("服务未提供平台目录，请检查 uni-api 版本与权限。");
  if (!keys.can_inspect_all)
    throw new ApiError(
      "密钥没有平台查看权限，请使用配置中的第一个密钥或管理员密钥。",
      403,
    );
  return keys;
}

function AccountForm({ onConnect }: { onConnect: () => void }) {
  const [username, setUsername] = useState(""),
    [password, setPassword] = useState(""),
    [pending, setPending] = useState(false),
    [error, setError] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setPending(true);
    setError("");
    try {
      await controlRequest("/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      setPassword("");
      onConnect();
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 401
          ? "用户名或密码错误。"
          : e instanceof Error
            ? e.message
            : "登录失败",
      );
    } finally {
      setPending(false);
    }
  }
  return (
    <form className="connection-form" onSubmit={submit}>
      <label htmlFor="console-username">用户名</label>
      <div className="input-wrap">
        <KeyRound size={18} />
        <input
          id="console-username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          required
          disabled={pending}
        />
      </div>
      <label htmlFor="console-password">密码</label>
      <div className="input-wrap">
        <ShieldCheck size={18} />
        <input
          id="console-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          required
          disabled={pending}
        />
      </div>
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      <button className="button primary connect-button" disabled={pending}>
        {pending ? (
          <Spinner small />
        ) : (
          <>
            登录 <ArrowRight size={17} />
          </>
        )}
      </button>
      <p className="connection-help">
        账户会话保留 30 天。uni-api 密钥由服务端加密保存。
      </p>
    </form>
  );
}
function ConnectionForm({
  onConnect,
  initialBase = "",
  compact = false,
  initialError = "",
}: {
  onConnect: (connection: Connection, keys: Keys) => void;
  initialBase?: string;
  compact?: boolean;
  initialError?: string;
}) {
  const [base, setBase] = useState(initialBase),
    [key, setKey] = useState(""),
    [visible, setVisible] = useState(false);
  const [pending, setPending] = useState(false),
    [error, setError] = useState(initialError);
  const attempt = useRef<AbortController | null>(null);
  useEffect(() => () => attempt.current?.abort(), []);
  async function connect(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    setError("");
    setPending(true);
    const run = new AbortController();
    attempt.current = run;
    try {
      const connection = {
        base: cleanBase(base),
        key: key.trim(),
        session: crypto.randomUUID(),
      };
      if (!connection.key) throw new Error("请输入平台访问密钥。");
      const keys = await readKeys(connection, run.signal);
      if (run.signal.aborted) return;
      onConnect(connection, keys);
      setKey("");
    } catch (e) {
      if (!run.signal.aborted)
        setError(e instanceof Error ? e.message : "连接失败，请重试。");
    } finally {
      if (!run.signal.aborted) setPending(false);
    }
  }
  return (
    <form
      className={`connection-form ${compact ? "compact" : ""}`}
      onSubmit={connect}
    >
      <label htmlFor="service-address">
        服务地址 <span>UNI-API ENDPOINT</span>
      </label>
      <div className="input-wrap">
        <Globe2 size={18} />
        <input
          id="service-address"
          type="url"
          placeholder="https://api.example.com"
          value={base}
          onChange={(e) => setBase(e.target.value)}
          autoComplete="url"
          required
          disabled={pending}
          spellCheck={false}
        />
      </div>
      <label htmlFor="access-key">
        访问密钥 <span>PLATFORM KEY</span>
      </label>
      <div className="input-wrap">
        <KeyRound size={18} />
        <input
          id="access-key"
          type={visible ? "text" : "password"}
          placeholder="配置中的第一个密钥或管理员密钥"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          autoComplete="off"
          required
          disabled={pending}
          spellCheck={false}
        />
        <button
          type="button"
          className="icon-button"
          onClick={() => setVisible(!visible)}
          aria-label={visible ? "隐藏密钥" : "显示密钥"}
        >
          {visible ? <EyeOff size={17} /> : <Eye size={17} />}
        </button>
      </div>
      <p className="field-note">
        <ShieldCheck size={14} />
        密钥保存在当前标签页会话中，断开连接时清除。
      </p>
      {error && (
        <div role="alert" className="error-banner">
          <TriangleAlert size={17} />
          <span>{error}</span>
        </div>
      )}
      <button className="button primary connect-button" disabled={pending}>
        {pending ? (
          <>
            <Spinner small /> 正在验证连接
          </>
        ) : (
          <>
            进入控制台 <ArrowRight size={17} />
          </>
        )}
      </button>
      <p className="connection-help">使用普通业务密钥无法查看平台数据。</p>
    </form>
  );
}

const reveal = {
  initial: { opacity: 0, y: 12 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.32 },
};

function Welcome({
  onConnect,
  error,
  legacyForm,
}: {
  onConnect: () => void;
  error: string;
  legacyForm?: ReactNode;
}) {
  return (
    <div className="welcome">
      <header className="welcome-header">
        <Brand />
        <a
          className="text-link"
          href="https://github.com/yym68686/uni-api-web"
          target="_blank"
          rel="noreferrer"
        >
          GitHub <ArrowUpRight size={16} />
        </a>
      </header>
      <main className="welcome-main">
        <motion.section {...reveal} className="welcome-intro">
          <span className="eyebrow">
            <span className="dot" /> YOUR MODELS. ONE CLEAR VIEW.
          </span>
          <h1>
            看清每一条路由。
            <br />
            <span>掌控每一次请求。</span>
          </h1>
          <p className="welcome-description">
            将模型、渠道与请求表现连在一起。
            <br />
            用一个清晰的工作台，发现等待、定位异常、了解余额。
          </p>
          <div className="route-visual" aria-hidden="true">
            <div className="route-line line-one" />
            <div className="route-line line-two" />
            <div className="visual-label">A CLEARER PATH FOR EVERY REQUEST</div>
            <div className="route-node origin">
              <Command size={22} />
              <span>应用请求</span>
            </div>
            <div className="route-node gateway">
              <Brand compact />
              <span>uni-api</span>
              <small>路由观测</small>
            </div>
            <div className="route-destinations">
              {["首字延迟", "请求前等待", "渠道余额"].map((label, i) => (
                <div className="route-node destination" key={label}>
                  <span className={`node-dot tone-${i}`} />
                  {label}
                  <Check size={14} />
                </div>
              ))}
            </div>
            <div className="visual-footer">
              <Layers3 size={14} /> 多模型 · 多渠道 · 一个视图
            </div>
          </div>
          <div className="welcome-features">
            <span>
              <Gauge size={17} />
              清晰的性能指标
            </span>
            <span>
              <Wallet size={17} />
              独立余额查询
            </span>
            <span>
              <ShieldCheck size={17} />
              安全连接你的服务
            </span>
          </div>
        </motion.section>
        <motion.section
          {...reveal}
          transition={{ delay: 0.09, duration: 0.35 }}
          className="connect-card"
        >
          <div className="connect-icon">
            <Link2 size={23} />
          </div>
          <h2>登录 uni-api console</h2>
          <p>使用账户管理多个 uni-api 来源，数据统一呈现。</p>
          {legacyForm || <AccountForm onConnect={onConnect} />}
          {error && (
            <p role="alert" className="error-banner">
              {error}
            </p>
          )}
          <div className="connect-card-footer">
            <span className="tiny-dot" /> 多来源分析 · 安全账户会话
          </div>
        </motion.section>
      </main>
      <footer className="welcome-footer">
        <span>
          uni-api console <span className="muted">/</span>{" "}
          为每一次模型调用带来清晰视野
        </span>
        <span>OPEN SOURCE · BUILT FOR CLARITY</span>
      </footer>
    </div>
  );
}

function MetricCard({
  label,
  value,
  sub,
  icon,
  accent = false,
}: {
  label: string;
  value: string;
  sub: string;
  icon: ReactNode;
  accent?: boolean;
}) {
  return (
    <div className={`metric-card ${accent ? "accent" : ""}`}>
      <div className="metric-card-label">
        {label}
        {icon}
      </div>
      <div className="metric-card-number">{value}</div>
      <p>{sub}</p>
    </div>
  );
}

function Trend({
  connection,
  keyId,
  window,
  model,
  refresh,
  endpoint,
  stream,
}: {
  connection: Connection;
  keyId: string;
  window: string;
  model: string;
  refresh: number;
  endpoint: string;
  stream: string;
}) {
  const series = useQuery({
    queryKey: [
      "trend",
      connection.session,
      keyId,
      window,
      model,
      refresh,
      endpoint,
      stream,
    ],
    queryFn: async ({ signal }) => {
      const analytic = await readMetrics(
        connection,
        "/v1/channel-metrics/timeseries?" +
          channelParams(keyId, window, model, endpoint, stream),
        signal,
        endpoint,
        stream,
      );
      return analytic;
    },
    refetchInterval: initializationRetryInterval,
  });
  const points = useMemo(() => {
    const buckets = new Map<
      number,
      { success: number; failed: number; covered: boolean }
    >();
    for (const row of series.data?.data || [])
      for (const p of row.points || []) {
        const b = buckets.get(p.timestamp) || {
          success: 0,
          failed: 0,
          covered: p.covered,
        };
        b.success += p.success;
        b.failed += p.failed;
        b.covered &&= p.covered;
        buckets.set(p.timestamp, b);
      }
    const sorted = [...buckets].sort(([a], [b]) => a - b);
    // Keep the chart responsive for broad windows while retaining the exact
    // aggregate query for the table and dashboard totals.
    const maxPoints = 120;
    if (sorted.length <= maxPoints) return sorted;
    const stride = Math.ceil(sorted.length / maxPoints);
    return sorted
      .filter((_, index) => index % stride === 0)
      .slice(0, maxPoints);
  }, [series.data]);
  const max = Math.max(1, ...points.map(([, p]) => p.success + p.failed));
  return (
    <motion.section {...reveal} className="trend-panel">
      <div className="panel-heading">
        <div>
          <span className="eyebrow">TRAFFIC AT A GLANCE</span>
          <h3>渠道尝试趋势</h3>
        </div>
        <div className="chart-legend">
          <span>
            <i className="legend-dot success" />
            成功
          </span>
          <span>
            <i className="legend-dot failure" />
            失败
          </span>
          <span>每分钟</span>
        </div>
      </div>
      {series.isPending ? (
        <div className="chart-skeleton skeleton" />
      ) : series.error instanceof AnalyticsInitializingError ? (
        <p role="status"><Spinner small /> {series.error.message}</p>
      ) : series.isError ? (
        <div className="inline-error">
          趋势暂时不可用。
          <button onClick={() => void series.refetch()}>重试</button>
        </div>
      ) : !points.length ? (
        <p className="muted">当前窗口暂无趋势数据。</p>
      ) : (
        <>
          <div className="chart-area">
            <div className="chart-axis">
              <span>{max}</span>
              <span>{Math.round(max / 2)}</span>
              <span>0</span>
            </div>
            <div className="bar-chart">
              {points.map(([at, p]) => (
                <Tip
                  key={at}
                  text={`${time(at)} · 成功 ${p.success} / 失败 ${p.failed}${p.covered ? "" : " · 覆盖不完整"}`}
                >
                  <span
                    className={`chart-column ${!p.covered ? "partial" : ""}`}
                    style={{ height: "100%" }}
                  >
                    <span
                      className="chart-stack"
                      style={{
                        height: `${Math.max(1, ((p.success + p.failed) / max) * 100)}%`,
                      }}
                    >
                      <span className="bar-failed" style={{ flex: p.failed }} />
                      <span
                        className="bar-success"
                        style={{ flex: p.success }}
                      />
                    </span>
                  </span>
                </Tip>
              ))}
            </div>
          </div>
          <div className="chart-times">
            <span>{time(points[0]?.[0])}</span>
            <span>所选 key / 模型范围 · 重试分别计数</span>
            <span>{time(points.at(-1)?.[0])}</span>
          </div>
        </>
      )}
    </motion.section>
  );
}

function Detail({
  row,
  onClose,
  balance,
  imports,
  catalog,
  site,
  check,
  trend,
}: {
  site?: string;
  check?: ChannelCheck;
  trend: Omit<CacheTrendProps, "row">;
  row: Channel | null;
  onClose: () => void;
  balance?: Balance;
  imports?: SubImportsQuery;
  catalog: Channel[];
}) {
  const installed = imports?.data?.data.find((item) => item.source_id === row?.source_id && item.provider === row?.provider);
  return (
    <Dialog.Root
      open={!!row}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="detail-panel">
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭详情"
          >
            <X size={20} />
          </Dialog.Close>
          <span className="eyebrow">CHANNEL INSIGHTS</span>
          <Dialog.Title><SiteLink base={site}>{row ? channelName(row) : ""}</SiteLink></Dialog.Title>
          <Dialog.Description className="detail-description">
            {row?.model} <ArrowRight size={13} /> {row?.upstream_model}
          </Dialog.Description>
          {row && (
            <>
              <Status row={row} />
              {trend.connection.account && <ChannelAccess key={providerId(row)} row={row} imports={imports} onRemoved={onClose} />}
              <CacheTrend key={`${providerId(row)}:${row.model}`} row={row} {...trend} />
              {row.source_id && trend.connection.account && <section className="detail-section">
                <h3>降智检测</h3>
                {check ? <div className="quality-status-stack"><Tip text={check.history && check.history.total > 0 ? qualityTooltip(check.history, check.checked_at, check.origin) : `最近检测：${new Date(check.checked_at * 1000).toLocaleString("zh-CN", { hour12: false })}`}><CheckVerdict result={check} /></Tip><QualityProbability history={check.history} checkedAt={check.checked_at} origin={check.origin} /></div> : <p className="muted">暂无检测结果。</p>}
                <QualityHistory key={providerId(row)} path={`/v1/sources/${encodeURIComponent(row.source_id)}/channel-checks/history?${new URLSearchParams({ provider: row.provider })}`} revision={`${check?.checked_at}:${check?.history?.total}:${check?.history?.successful}`} title="渠道检测记录" />
                {boundGroups(installed).map(group => <QualityHistory key={`${group.account_id}:${group.group_id}`} path={`/v1/sub2api/accounts/${encodeURIComponent(group.account_id)}/groups/${group.group_id}/quality-history`} revision={`${check?.checked_at}:${check?.history?.total}:${check?.history?.successful}`} title={`sub2api 检测记录 · 分组 #${group.group_id}`} />)}
              </section>}
              {imports && <ChannelModels row={row} imports={imports} catalog={catalog} />}
              <div className="detail-section">
                <h3>请求时间线</h3>
                <p className="muted">两个指标分别统计，分位值不能直接相加。</p>
                <div className="timing-track">
                  <div>
                    <span className="track-dot" />
                    <small>进入 uni-api</small>
                    <strong>0 ms</strong>
                  </div>
                  <div>
                    <span className="track-dot" />
                    <small>发起当前渠道</small>
                    <strong>
                      {ms(row.stats?.request_to_dispatch?.p50_ms)}
                    </strong>
                    <span>请求前等待 p50</span>
                  </div>
                  <div>
                    <span className="track-dot end" />
                    <small>响应已创建</small>
                    <strong>
                      + <ResponseLatency created={row.stats?.response_created?.p50_ms} text={row.stats?.first_text?.p50_ms} />
                    </strong>
                    <span>渠道首字 p50</span>
                  </div>
                </div>
                <dl className="detail-stats">
                  <div>
                    <dt>请求前等待 p95</dt>
                    <dd>{ms(row.stats?.request_to_dispatch?.p95_ms)}</dd>
                  </div>
                  <div>
                    <dt>最近一次请求前等待</dt>
                    <dd>{ms(row.stats?.request_to_dispatch?.last_ms)}</dd>
                  </div>
                  <div>
                    <dt>已发起计时样本</dt>
                    <dd>
                      {count(row.stats?.request_to_dispatch?.sample_count || 0)}
                    </dd>
                  </div>
                  <div>
                    <dt>最近成功</dt>
                    <dd>{time(row.stats?.last_success_at)}</dd>
                  </div>
                  <div>
                    <dt>首字延迟 p95</dt>
                    <dd>{ms(row.stats?.response_created?.p95_ms)}</dd>
                  </div>
                  <div>
                    <dt>最近一次首字延迟</dt>
                    <dd>{ms(row.stats?.response_created?.last_ms)}</dd>
                  </div>
                </dl>
              </div>
              <div className="detail-section">
                <h3>尝试结果</h3>
                <div className="result-grid">
                  <div>
                    <strong>{count(row.stats?.success || 0)}</strong>
                    <span>成功</span>
                  </div>
                  <div>
                    <strong>{count(row.stats?.failed || 0)}</strong>
                    <span>失败</span>
                  </div>
                  <div>
                    <strong>{rate(row.stats?.success_rate)}</strong>
                    <span>成功率</span>
                  </div>
                </div>
                <p className="muted">
                  成功率分母为已成功或失败的上游尝试，不等同于用户请求数。
                </p>
              </div>
              <div className="detail-section">
                <h3>上游余额 / 额度</h3>
                <BalanceValue balance={balance} loading={!balance} detail />
                <p className="muted">
                  余额由上游提供，最多缓存 5
                  分钟。负余额不自动改变路由可用状态。
                </p>
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Guide({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog">
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭指标说明"
          >
            <X size={20} />
          </Dialog.Close>
          <span className="eyebrow">READ THE SIGNAL</span>
          <Dialog.Title>让每一个数字都有意义</Dialog.Title>
          <Dialog.Description>
            控制台展示当前 uni-api 实例的数据，帮助你理解渠道表现。
          </Dialog.Description>
          <div className="guide-items">
            {[
              [
                "渠道尝试",
                "一次用户请求可能依次尝试多个渠道。成功率 = 成功 ÷（成功 + 失败），跳过和取消不计入分母。",
              ],
              [
                "请求前等待",
                "从请求进入 uni-api，到发起当前渠道 HTTP 请求前。包含请求体读取、排队和前序重试；p50 反映窗口内的典型等待。",
              ],
              [
                "首字延迟",
                "当前渠道请求开始到首个 response.created 的耗时；tooltip 另列首个 response.output_text.delta 的延迟。未采集创建事件的历史或非流式请求显示缺失，与请求前等待分别聚合，分位值不可直接相加。p50 / p95 是直方图上界估计。",
              ],
              [
                "余额不足",
                "余额或额度 ≤ 0；多密钥渠道须全部已查询且均不足。未适配、失败和未知不算不足，账户共享余额不可相加。",
              ],
              [
                "范围与保留",
                "默认统计全部端点和全部流式状态，可分别筛选。跨组延迟由后端合并直方图后计算。默认按 provider 配置顺序；选择 API key 后按该 key 配置顺序，历史请求、尝试、延迟、Token 和估算消费仅统计该 key 发起的请求。渠道状态、总并发、余额及上游实际消费为渠道整体数据。历史事实存储于 S3，由 DuckDB 聚合；采集开始之前的流量无法补回。",
              ],
            ].map(([title, text]) => (
              <section key={title}>
                <h3>{title}</h3>
                <p>{text}</p>
              </section>
            ))}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Dashboard({
  connection: baseConnection,
  disconnect,
  changeConnection,
}: {
  connection: Connection;
  disconnect: (reason?: string) => void;
  changeConnection: () => void;
}) {
  const [filters, setFilters] = useState(() =>
    loadFilters(baseConnection.base),
  );
  const sourceQuery = useQuery({
    queryKey: ["sources", baseConnection.session],
    queryFn: () => controlRequest<{ data: ConsoleSource[] }>("/v1/sources"),
    enabled: !!baseConnection.account,
  });
  const sourceList = sourceQuery.data?.data || [];
  const imported = useSubImports(!!baseConnection.account);
  const rawChecks = useChannelChecks(
    baseConnection.session,
    !!baseConnection.account,
  );
  const subQuality = useSubQualitySummary(!!baseConnection.account);
  const checks = withSubQuality(rawChecks, imported.data?.data || [], subQuality.data?.data || []);
  const siteFor = useChannelSites(baseConnection, imported.data?.data || []);
  const selectedSourceId = filters.sourceId;
  const connection = useMemo(
    () => ({
      ...baseConnection,
      sourceId: selectedSourceId || undefined,
      session: baseConnection.account
        ? baseConnection.session + ":" + (selectedSourceId || "all")
        : baseConnection.session,
    }),
    [baseConnection, selectedSourceId],
  );
  const {
    keyId,
    model,
    window,
    balanceFilter,
    statusFilter,
    search,
    sort,
    endpoint,
    stream,
  } = filters;
  const [view, setView] = useState<View>(() =>
      loadView(baseConnection.base, !!baseConnection.account),
    ),
    [page, setPage] = useState(0),
    [adjustingChannels, setAdjustingChannels] = useState(false),
    [checkingChannels, setCheckingChannels] = useState(false);
  const channelView = view === "channels";
  useEffect(() => {
    saveView(baseConnection.base, view);
  }, [baseConnection.base, view]);
  useEffect(() => {
    saveFilters(baseConnection.base, filters);
  }, [connection.base, filters]);
  const hasFilters = (Object.keys(defaultFilters) as (keyof Filters)[]).some(
    (key) => filters[key] !== defaultFilters[key],
  );
  const [detailId, setDetailId] = useState<string | null>(null),
    [showTrend, setShowTrend] = useState(false),
    [guide, setGuide] = useState(false),
    [menu, setMenu] = useState(false),
    [refresh, setRefresh] = useState(0);

  const [auto, setAuto] = useState(false);
  const [theme, setTheme] = useTheme();
  const deferredSearch = useDeferredValue(search);
  const keys = useQuery({
    enabled: !baseConnection.account || !!sourceQuery.data,
    queryKey: ["keys", connection.session],
    queryFn: ({ signal }) => readKeys(connection, signal),
  });
  useEffect(() => {
    if (
      keys.error instanceof ApiError &&
      [401, 403].includes(keys.error.status)
    )
      disconnect(keys.error.message);
  }, [keys.error, disconnect]);
  const keyRemoved =
    !!keyId &&
    !!keys.data &&
    !keys.data.data.some((item) => item.key_id === keyId);
  const keysLoaded = !!keys.data;
  const params = channelParams(keyId, window, "", endpoint, stream);
  const catalog = useQuery({
    queryKey: ["catalog", connection.session, keyId, endpoint, stream],
    queryFn: ({ signal }) =>
      request<Catalog>(connection, "/v1/model-channels?" + params, signal),
    enabled:
      keysLoaded &&
      !keyRemoved &&
      (!baseConnection.account || sourceList.length > 0),
  });
  const prices = useQuery({
    queryKey: ["prices", connection.session],
    queryFn: ({ signal }) =>
      analyticsRequest<{ data: ModelPrice[] }>(
        connection,
        "/analytics/v1/prices",
        signal,
      ),
    enabled: keysLoaded && view === "prices",
    refetchInterval: initializationRetryInterval,
    staleTime: 60_000,
  });
  const metrics = useQuery({
    queryKey: ["metrics", connection.session, keyId, window, endpoint, stream],
    queryFn: ({ signal }) =>
      readMetrics(
        connection,
        "/v1/channel-metrics?" + params,
        signal,
        endpoint,
        stream,
      ),
    staleTime: 30_000,
    enabled: keysLoaded && !keyRemoved && !!catalog.data,
    refetchInterval: (query) => initializationRetryInterval(query) || (auto ? 30_000 : false),
    refetchIntervalInBackground: false,
  });
  const liveMetrics = useQuery({
    queryKey: ["live-metrics", connection.session, keyId, endpoint, stream],
    queryFn: ({ signal }) =>
      request<Metrics>(
        connection,
        "/v1/channel-metrics?" +
          channelParams(keyId, "1m", "", endpoint, stream),
        signal,
      ),
    enabled:
      keysLoaded &&
      !keyRemoved &&
      !!catalog.data &&
      (view === "overview" || channelView),
    staleTime: 2_000,
    refetchInterval: auto ? 5_000 : false,
    refetchIntervalInBackground: false,
  });
  const staleSources = useMemo(
    () => staleHistorySources(metrics.data, liveMetrics.data),
    [metrics.data, liveMetrics.data],
  );
  const staleSourceNames = sourceList
    .filter((source) => staleSources.has(source.id))
    .map((source) => source.name);
  const liveMap = useMemo(
    () =>
      new Map(
        (liveMetrics.data?.data || []).map((row) => [
          rowId(row),
          row.stats?.inflight,
        ]),
      ),
    [liveMetrics.data],
  );
  const queryClient = useQueryClient();
  useEffect(() => {
    if (!keysLoaded || keyRemoved || !catalog.data || !channelView || !metrics.isSuccess) return;
    const windows = ranges.map(([value]) => value);
    const controller = new AbortController();
    void (async () => {
      // Prefetch ranges concurrently. Sequential prefetch made the last
      // windows wait behind every earlier range, so switching to month/year/all
      // could miss the one-second interaction target even though each DuckDB
      // query itself was fast.
      await Promise.all(
        windows
          .filter((next) => next !== window)
          .map(async (next) => {
            if (controller.signal.aborted) return;
            const key = [
              "metrics",
              connection.session,
              keyId,
              next,
              endpoint,
              stream,
            ];
            if (queryClient.getQueryData(key)) return;
            await queryClient.prefetchQuery({
              queryKey: key,
              queryFn: ({ signal }) =>
                readMetrics(
                  connection,
                  "/v1/channel-metrics?" +
                    channelParams(keyId, next, "", endpoint, stream),
                  signal,
                  endpoint,
                  stream,
                ),
              staleTime: 30_000,
            });
          }),
      );
    })();
    return () => controller.abort();
  }, [
    keysLoaded,
    keyRemoved,
    catalog.data,
    metrics.isSuccess,
    view,
    connection,
    keyId,
    endpoint,
    stream,
    window,
    queryClient,
  ]);
  const models = useMemo(
    () => [...new Set(catalog.data?.data.map((row) => row.model) || [])].sort(),
    [catalog.data],
  );
  const endpoints = [
    ...new Set([
      ...endpointChoices,
      ...(metrics.data?.available_endpoints || []),
      ...(endpoint === "all" ? [] : [endpoint]),
    ]),
  ].sort();
  const modelRemoved = !!model && !!catalog.data && !models.includes(model);
  const sourceRemoved =
    !!selectedSourceId &&
    !!sourceQuery.data &&
    !sourceList.some((source) => source.id === selectedSourceId);
  const historyInitializing = metrics.error instanceof AnalyticsInitializingError;
  const error =
    sourceQuery.error?.message ||
    (sourceRemoved ? "所选来源已移除，请重新选择。" : "") ||
    (keyRemoved
      ? "所选 API key 已移除，请重新选择。"
      : modelRemoved
        ? "当前 API key 未配置所选模型，请重新选择模型。"
        : keys.error?.message ||
          (historyInitializing ? undefined : metrics.error?.message) ||
          catalog.error?.message);
  const rows = useMemo(
    () =>
      error
        ? []
        : catalogMetrics(catalog.data, metrics.data)
            .filter((row) => !model || row.model === model)
            .map((row) => ({
              ...row,
              provider_name:
                imported.data?.labels?.[row.source_id || ""]?.[row.provider],
            })),
    [catalog.data, metrics.data, model, error, imported.data],
  );
  const controls = useChannelControls({
    connection: baseConnection,
    rows,
    sourceIds: sourceList
      .filter(
        (source) =>
          (!selectedSourceId || source.id === selectedSourceId) &&
          (!keyId.includes("::") || source.id === keyId.split("::")[0]),
      )
      .map((source) => source.id),
    keyId,
    model,
    enabled: !!baseConnection.account && channelView,
  });
  const tableRows =
    channelView && sort === "config" ? controls.arrange(rows) : rows;
  const rowRanks = useMemo(
    () => new Map(tableRows.map((row, i) => [rowId(row), i + 1])),
    [tableRows],
  );
  const providers = useMemo(() => [...new Set(rows.map(providerId))], [rows]);
  const actualRange = useMemo(() => actualCostRange(window), [window]);
  const channelSpend = useSubChannelSpend({ providers, imports: imported.data?.data || [], session: connection.session, window, to: metrics.data?.to, refresh, auto, enabled: channelView && !!baseConnection.account });
  const limit = useMemo(() => makeLimiter(3), []);
  const accountBalances = useChannelAccountBalances(providers, imported.data?.data || [], connection.session, !!baseConnection.account, auto);
  const rawBalanceQueries = useQueries({
    queries: providers.map((provider) => ({
      queryKey: [
        "balance",
        connection.session,
        metrics.data?.snapshot_revision,
        provider,
        model,
        window,
      ],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        limit(
          () =>
            request<Balance>(
              {
                ...connection,
                sourceId: JSON.parse(provider)[0] || connection.sourceId,
              },
              "/v1/channel-balances?" +
                new URLSearchParams({
                  provider: JSON.parse(provider)[1],
                  ...(model ? { model } : {}),
                  ...(actualRange.startDate
                    ? { start_date: actualRange.startDate }
                    : {}),
                  ...(actualRange.endDate
                    ? { end_date: actualRange.endDate }
                    : {}),
                }),
              signal,
            ),
          signal,
        ),
      staleTime: 300_000,
      enabled: !accountBalances.has(provider),
      refetchInterval: auto ? 300_000 : false,
      refetchIntervalInBackground: false,
      retry: false,
    })),
  });
  const balanceQueries = providers.map((provider,i) => accountBalances.get(provider) || rawBalanceQueries[i]);
  const balanceMap = new Map(
    providers.map((provider, i) => [provider, balanceQueries[i]]),
  );
  const pendingBalances = balanceQueries.filter(
    (query) => query.isPending,
  ).length;
  const lowCount = balanceQueries.filter((query) =>
    balanceIsLow(query.data),
  ).length;
  const stats = summarize(rows);
  const filtered = tableRows.filter(
    (row) =>
      (!deferredSearch ||
        `${channelName(row)} ${row.provider} ${row.model} ${row.upstream_model}`
          .toLowerCase()
          .includes(deferredSearch.toLowerCase())) &&
      (!balanceFilter || balanceIsLow(balanceMap.get(providerId(row))?.data)) &&
      (!statusFilter ||
        (statusFilter === "eligible" ? row.eligible : !row.eligible)),
  );
  if (sort !== "config")
    filtered.sort((a, b) => {
      const values = (row: Channel) =>
        sort === "success"
          ? row.stats?.success_rate == null
            ? null
            : -row.stats.success_rate
          : sort === "latency"
            ? row.stats?.response_created?.p50_ms
            : row.stats?.request_to_dispatch?.p50_ms;
      return (values(a) ?? Infinity) - (values(b) ?? Infinity);
    });
  const balanceProviders = [...new Set(filtered.map(providerId))];
  const detectionRows = checkTargets(filtered);
  const total = channelView ? filtered.length : balanceProviders.length;
  const pageCount = Math.max(1, Math.ceil(total / 25)),
    currentPage = Math.min(page, pageCount - 1);
  const pageRows = filtered.slice(currentPage * 25, (currentPage + 1) * 25),
    pageProviders = balanceProviders.slice(
      currentPage * 25,
      (currentPage + 1) * 25,
    );
  const detail = rows.find((row) => rowId(row) === detailId) || null;
  const busy = metrics.isFetching || catalog.isFetching || keys.isFetching;
  function setFilter(name: keyof Filters, value: string) {
    setPage(0);
    setFilters((current) => ({ ...current, [name]: value }));
  }
  function reload() {
    if (
      baseConnection.account &&
      (view === "sources" || sourceList.length === 0)
    ) {
      void sourceQuery.refetch();
      void queryClient.invalidateQueries({ queryKey: ["control-persistence"] });
      return;
    }
    if (view === "prices") {
      void prices.refetch();
      return;
    }
    if (view === "sub2api") {
      void queryClient.invalidateQueries({ queryKey: ["sub2api"] });
      return;
    }
    if (channelView && baseConnection.account && !controls.pending) {
      void queryClient.invalidateQueries({ queryKey: ["channel-controls"] });
      void queryClient.invalidateQueries({ queryKey: ["control-catalog"] });
    }
    if (baseConnection.account) { void checks.refetch(); void subQuality.refetch(); }
    void keys.refetch();
    void liveMetrics.refetch();
    if (
      keysLoaded &&
      !keyRemoved &&
      (!connection.account || sourceList.length > 0)
    ) {
      void catalog.refetch();
      void metrics.refetch();
    }
    for (const query of balanceQueries) void query.refetch();
    setRefresh((x) => x + 1);
  }
  const refreshBusy =
    baseConnection.account && (view === "sources" || sourceList.length === 0)
      ? sourceQuery.isFetching
      : view === "prices"
        ? prices.isFetching
        : busy;
  const refreshButton = (
    <button
      className={`button small ${refreshBusy ? "refreshing" : ""}`}
      onClick={reload}
      disabled={refreshBusy}
    >
      <RefreshCw size={15} className={refreshBusy ? "spin" : ""} />
      刷新数据
    </button>
  );
  function selectView(next: View) {
    setView(next);
    setPage(0);
    setMenu(false);
  }
  const nav = (
    <ConsoleNavigation
      view={view}
      account={!!baseConnection.account}
      lowCount={lowCount}
      onSelect={selectView}
      onGuide={() => {
        setGuide(true);
        setMenu(false);
      }}
    />
  );
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "1") {
        e.preventDefault();
        selectView("channels");
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, []);
  return (
    <div className="app-shell">
      <aside className="sidebar">{nav}</aside>
      <Dialog.Root open={menu} onOpenChange={setMenu}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="mobile-sidebar">
            <Dialog.Title className="sr-only">主导航</Dialog.Title>
            <Dialog.Description className="sr-only">
              选择控制台视图
            </Dialog.Description>
            <Dialog.Close
              className="icon-button mobile-menu-close"
              aria-label="关闭菜单"
            >
              <X size={20} />
            </Dialog.Close>
            {nav}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      <div className="main-shell">
        <ConsoleHeader
          view={view}
          theme={theme}
          onMenu={() => setMenu(true)}
          onTheme={() => setTheme(theme === "dark" ? "light" : "dark")}
          onConnection={() =>
            baseConnection.account ? selectView("sources") : changeConnection()
          }
        />
        <main className="workspace">
          {!!catalog.data?.unavailable_sources?.length && (
            <div role="alert" className="error-banner">
              来源暂不可用：{catalog.data.unavailable_sources.join("、")}
              。当前结果不完整。
            </div>
          )}
          {historyInitializing && (channelView || view === "overview") && (
            <div className="coverage-note" role="status">
              <Spinner small />
              {metrics.data
                ? `历史数据正在初始化，暂显示上次成功读取的数据（${time(metrics.data.generated_at)}），完成后将自动更新。`
                : metrics.error?.message}
            </div>
          )}
          {view === "sub2api" && baseConnection.account ? (
            <Sub2apiChecks
              key={baseConnection.session}
              user={baseConnection.session}
            />
          ) : baseConnection.account &&
            (view === "sources" ||
              (sourceQuery.isSuccess && sourceList.length === 0)) ? (
            <SourceSettings
              sources={sourceList}
              refreshAction={refreshButton}
              onSaved={() => {
                void sourceQuery.refetch();
                void queryClient.invalidateQueries({ queryKey: ["keys"] });
                void queryClient.invalidateQueries({ queryKey: ["catalog"] });
                void queryClient.invalidateQueries({ queryKey: ["metrics"] });
              }}
            />
          ) : view === "automations" && baseConnection.account ? (
            <Automation sources={sourceList} models={models} refreshAction={refreshButton} />
          ) : view === "prices" ? (
            <PriceSettings
              prices={prices.data?.data || []}
              refreshAction={refreshButton}
              loading={prices.isPending}
              error={prices.error?.message}
              connection={connection}
              onSaved={() => void prices.refetch()}
            />
          ) : historyInitializing && !metrics.data ? null : view === "overview" ? (
            <Overview
              metrics={metrics.data}
              rows={rows}
              live={liveMap}
              refreshAction={refreshButton}
            />
          ) : (
            <motion.section
              {...reveal}
              transition={{ delay: 0.04 }}
              className="metric-grid"
            >
              <MetricCard
                label="观测渠道"
                value={count(stats.providers)}
                sub={`${count(new Set(rows.map((r) => r.model)).size)} 个模型 · 当前筛选范围`}
                icon={<Layers3 size={17} />}
              />
              <MetricCard
                label="可用渠道"
                value={`${stats.eligible} / ${stats.providers}`}
                sub={
                  endpoint === "all"
                    ? "按渠道整体冷却与凭据状态"
                    : "至少一个模型当前可路由"
                }
                icon={<Radio size={17} />}
                accent
              />
              <MetricCard
                label="渠道成功率"
                value={rate(stats.successRate)}
                sub={`${count(stats.success)} 成功 / ${count(stats.completed)} 次已完成尝试`}
                icon={<CheckCheck size={17} />}
              />
              <MetricCard
                label="余额不足"
                value={count(lowCount)}
                sub={
                  pendingBalances
                    ? `${pendingBalances} 个渠道余额查询中`
                    : "全部密钥均确认余额或额度不足"
                }
                icon={<Wallet size={17} />}
              />
            </motion.section>
          )}
          {channelView || view === "balances" ? (
            <section className="data-panel">
              <div className="data-heading">
                <div className="data-title">
                  <span className="section-icon">
                    {channelView ? (
                      <Activity size={19} />
                    ) : (
                      <Wallet size={19} />
                    )}
                  </span>
                  <h2>{channelView ? "渠道表现" : "渠道余额"}</h2>
                  <span className="count-badge">{count(total)}</span>
                </div>
                <div className="data-actions">
                  <div className="search-field">
                    <Search size={17} />
                    <input
                      aria-label="搜索渠道或模型"
                      placeholder="搜索渠道、模型…"
                      value={search}
                      onChange={(e) => setFilter("search", e.target.value)}
                    />
                    {search && (
                      <button
                        className="icon-button"
                        onClick={() => setFilter("search", "")}
                        aria-label="清除搜索"
                      >
                        <X size={14} />
                      </button>
                    )}
                  </div>
                  {channelView && baseConnection.account && (
                    <ChannelControlActions
                      controls={controls}
                      sources={sourceList}
                      keys={keys.data?.data || []}
                      editing={adjustingChannels}
                      onEditingChange={setAdjustingChannels}
                    />
                  )}
                  {channelView && baseConnection.account && (
                    <CheckActions
                      checks={checks}
                      rows={detectionRows}
                      expanded={checkingChannels}
                      onExpandedChange={setCheckingChannels}
                      disabled={
                        !baseConnection.account ||
                        busy ||
                        !!error ||
                        !!(balanceFilter && pendingBalances)
                      }
                    />
                  )}
                  {refreshButton}
                  {channelView && (
                    <button
                      className={`button small ghost ${showTrend ? "selected" : ""}`}
                      onClick={() => setShowTrend(!showTrend)}
                    >
                      <Activity size={15} />
                      {showTrend ? "收起趋势" : "查看趋势"}
                    </button>
                  )}
                  <label className="auto-refresh">
                    <input
                      type="checkbox"
                      checked={auto}
                      onChange={(e) => setAuto(e.target.checked)}
                    />
                    <span />
                    自动刷新
                  </label>
                  <Tip text="每 30 秒刷新渠道指标；余额最多缓存 5 分钟。页面后台暂停自动刷新。">
                    <CircleHelp size={14} className="muted" />
                  </Tip>
                </div>
              </div>
              <div className="filters">
                <div className="select-field time-select">
                  <Clock3 size={15} />
                  <select
                    aria-label="时间范围筛选"
                    value={window}
                    onChange={(e) => setFilter("window", e.target.value)}
                  >
                    {ranges.map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                  <ChevronDown size={13} />
                </div>
                {sourceList.length > 0 && (
                  <label className="select-field source-select">
                    <Server size={15} />
                    <select
                      aria-label="uni-api 来源"
                      value={selectedSourceId}
                      onChange={(e) => {
                        setFilter("sourceId", e.target.value);
                        setFilter("keyId", "");
                      }}
                    >
                      <option value="">全部来源</option>
                      {sourceList.map((source) => (
                        <option value={source.id} key={source.id}>
                          {source.name}
                        </option>
                      ))}
                    </select>
                    <ChevronDown size={13} />
                  </label>
                )}
                <div className="select-field">
                  <KeyRound size={15} />
                  <select
                    aria-label="API key 筛选"
                    value={keyId}
                    onChange={(e) => setFilter("keyId", e.target.value)}
                  >
                    <option value="">全部渠道 · 配置顺序</option>
                    {keyRemoved && (
                      <option value={keyId}>已移除的 API key</option>
                    )}
                    {keys.data?.data.map((key) => (
                      <option key={key.key_id} value={key.key_id}>
                        {key.source_name ? `${key.source_name} · ` : ""}Key{" "}
                        {key.position} · {key.prefix}
                      </option>
                    ))}
                  </select>
                  <ChevronDown size={13} />
                </div>
                <div className="select-field model-select">
                  <Layers3 size={15} />
                  <select
                    aria-label="模型筛选"
                    value={model}
                    onChange={(e) => setFilter("model", e.target.value)}
                  >
                    <option value="">全部模型</option>
                    {modelRemoved && (
                      <option value={model}>{model} · 未配置</option>
                    )}
                    {models.map((name) => (
                      <option key={name}>{name}</option>
                    ))}
                  </select>
                  <ChevronDown size={13} />
                </div>
                <div className="inline-select">
                  <Globe2 size={13} />
                  <select
                    aria-label="端点筛选"
                    value={endpoint}
                    onChange={(e) => setFilter("endpoint", e.target.value)}
                  >
                    <option value="all">全部端点</option>
                    {endpoints.map((path) => (
                      <option key={path} value={path}>
                        {path}
                      </option>
                    ))}
                  </select>
                  <ChevronDown size={12} />
                </div>
                <div className="inline-select">
                  <Radio size={13} />
                  <select
                    aria-label="流式状态筛选"
                    value={stream}
                    onChange={(e) => setFilter("stream", e.target.value)}
                  >
                    <option value="all">全部流式状态</option>
                    <option value="true">流式</option>
                    <option value="false">非流式</option>
                  </select>
                  <ChevronDown size={12} />
                </div>
                <button
                  className={`filter-chip ${balanceFilter ? "active" : ""}`}
                  aria-pressed={!!balanceFilter}
                  onClick={() =>
                    setFilter("balanceFilter", balanceFilter ? "" : "low")
                  }
                >
                  <Wallet size={13} />
                  余额不足{balanceFilter && <X size={12} />}
                </button>
                <div className="inline-select">
                  <Filter size={13} />
                  <select
                    aria-label="渠道状态筛选"
                    value={statusFilter}
                    onChange={(e) => setFilter("statusFilter", e.target.value)}
                  >
                    <option value="">全部状态</option>
                    <option value="eligible">可用渠道</option>
                    <option value="unavailable">不可用渠道</option>
                  </select>
                  <ChevronDown size={12} />
                </div>
                <div className="inline-select">
                  <SlidersHorizontal size={13} />
                  <select
                    aria-label="排序"
                    value={sort}
                    onChange={(e) => setFilter("sort", e.target.value)}
                  >
                    <option value="config">
                      {keyId ? "API key 顺序" : "Provider 顺序"}
                    </option>
                    <option value="success">成功率从高到低</option>
                    <option value="latency">首字延迟从低到高</option>
                    <option value="wait">请求前等待从低到高</option>
                  </select>
                  <ChevronDown size={12} />
                </div>
                {hasFilters && (
                  <button
                    className="filter-chip"
                    onClick={() => {
                      setFilters({ ...defaultFilters });
                      setPage(0);
                    }}
                  >
                    <X size={12} /> 重置筛选
                  </button>
                )}
              </div>
              {channelView && checks.error && (
                <div className="coverage-note" role="alert">
                  最近检测结果读取失败：{checks.error.message}
                </div>
              )}
              {!historyInitializing && staleSources.size > 0 && (
                <div className="coverage-note" role="alert">
                  <Clock3 size={14} />
                  {staleSourceNames.join("、") || "当前来源"}{" "}
                  仍有请求完成，但历史数据同步已滞后超过 2 分钟；当前统计可能遗漏请求，不能视为零流量。
                </div>
              )}
              {metrics.data?.import && !metrics.data.import.caught_up &&
                (!metrics.data.import.scanning || !!metrics.data.import.error_class || metrics.data.import.remaining_objects > 0) && (
                <div className="coverage-note" role="status">
                  <Clock3 size={14} />
                  {metrics.data.import.error_class
                    ? `采集异常：${metrics.data.import.error_class}`
                    : `正在同步历史事实，剩余 ${count(metrics.data.import.remaining_objects || 0)} 个对象；当前统计尚不完整。`}
                </div>
              )}
              {metrics.data?.coverage === "partial" && (
                <div className="coverage-note">
                  <Clock3 size={14} />
                  {metrics.data.dropped
                    ? "部分指标因容量限制未收集，请结合日志核对。"
                    : `实例于 ${time(metrics.data.collection_started_at)} 开始采集，当前窗口覆盖尚不完整。`}
                </div>
              )}
              {error ? (
                <div role="alert" className="table-error">
                  <Unplug size={26} />
                  <h3>暂时无法读取渠道</h3>
                  <p>{error}</p>
                  <button className="button" onClick={reload}>
                    重新读取
                  </button>
                </div>
              ) : metrics.isPending || (historyInitializing && !metrics.data) ? (
                <div className="table-skeleton" aria-label="加载渠道">
                  <div className="skeleton skeleton-header" />
                  {Array.from({ length: 7 }, (_, i) => (
                    <div className="skeleton skeleton-row" key={i} />
                  ))}
                </div>
              ) : total === 0 ? (
                <Empty
                  title={
                    pendingBalances && balanceFilter
                      ? "正在确认渠道余额"
                      : "没有匹配的渠道"
                  }
                  icon={<Search size={26} />}
                >
                  {pendingBalances && balanceFilter
                    ? `还有 ${pendingBalances} 个渠道正在查询，结果到达后会自动显示。`
                    : "尝试调整模型、余额状态，或清除搜索条件。"}
                </Empty>
              ) : channelView ? (
                <div className="table-scroll">
                  <table className="channel-table">
                    <thead>
                      <tr>
                        <th className="rank">#</th>
                        <th>渠道 / 模型</th>
                        {baseConnection.account && (
                          <th>
                            <Tip text="显示此来源、此渠道最近一次 gpt-6-astra 检测结果，与当前指标时间范围无关；不代表表中每个模型都已单独检测。">
                              是否降智 <CircleHelp size={12} />
                            </Tip>
                          </th>
                        )}
                        {checkingChannels && <th>检测操作</th>}
                        <ChannelMetricHeaders keySelected={!!keyId} />
                        {adjustingChannels && (
                          <th>
                            <Tip text="修改作用于本行来源、当前选择的 API key 和模型（未选择则为全部），对所有端点和流式状态生效。无到期时间；来源设置开启“保留临时配置”时，重启后自动恢复已应用的更改。">
                              临时控制 <CircleHelp size={12} />
                            </Tip>
                          </th>
                        )}
                        <th aria-label="详情" />
                      </tr>
                    </thead>
                    <tbody>
                      {pageRows.map((row) => {
                        const balance = balanceMap.get(providerId(row));
                        return (
                          <tr key={rowId(row)}>
                            <td className="rank mono">
                              {String(rowRanks.get(rowId(row))).padStart(
                                2,
                                "0",
                              )}
                            </td>
                            <td>
                              <button
                                className="channel-link"
                                onClick={() => setDetailId(rowId(row))}
                              >
                                <span className="provider-avatar">
                                  {channelName(row).slice(0, 1).toUpperCase()}
                                </span>
                                <span>
                                  <strong>{channelName(row)}</strong>
                                  {row.source_name && (
                                    <small className="source-label">
                                      {row.source_name}
                                    </small>
                                  )}
                                  <small>{row.model}</small>
                                </span>
                              </button>
                            </td>
                            {baseConnection.account && (
                              <LatestChannelCheck row={row} checks={checks} />
                            )}
                            {checkingChannels && (
                              <ChannelCheckAction row={row} checks={checks} />
                            )}
                            <ChannelMetricCells
                              row={row}
                              inflight={liveMap.get(rowId(row))}
                              balance={balance}
                              actualRange={actualRange}
                              spend={channelSpend.get(providerId(row))}
                              importedChannel={!!baseConnection.account && row.provider.startsWith("sub2api-")}
                              stale={staleSources.has(row.source_id || "")}
                            />
                            {adjustingChannels && (
                              <ChannelControlCell
                                row={row}
                                controls={controls}
                                visible={filtered}
                                configOrder={sort === "config"}
                              />
                            )}
                            <td>
                              <button
                                className="row-arrow icon-button"
                                onClick={() => setDetailId(rowId(row))}
                                aria-label={`查看 ${channelName(row)} ${row.model} 详情`}
                              >
                                <ArrowUpRight size={16} />
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="balance-grid">
                  {pageProviders.map((provider) => {
                    const balance = balanceMap.get(provider);
                    const providerName = filtered.find(
                      (row) => providerId(row) === provider,
                    );
                    const displayName = providerName
                      ? channelName(providerName)
                      : JSON.parse(provider)[1];
                    const sourceRow = rows.find((row) => providerId(row) === provider);
                    const sourceName = sourceRow?.source_name;
                    const sourceLink = siteFor(sourceRow) || dashboardURL(sourceList.find((source) => source.id === sourceRow?.source_id)?.base);
                    return (
                      <article
                        className={`balance-card ${balanceIsLow(balance?.data) ? "low-balance" : ""}`}
                        key={provider}
                      >
                        <div className="balance-card-top">
                          <span className="provider-avatar">
                            {displayName[0].toUpperCase()}
                          </span>
                          <span>
                            <strong><SiteLink base={sourceLink}>{displayName}</SiteLink></strong>
                            <small>{sourceName}</small>
                            <small>
                              {
                                rows.filter(
                                  (row) => providerId(row) === provider,
                                ).length
                              }{" "}
                              个模型
                            </small>
                          </span>
                          {balanceIsLow(balance?.data) && (
                            <span className="status-pill cooling">
                              余额不足
                            </span>
                          )}
                        </div>
                        <BalanceValue
                          balance={balance?.data}
                          loading={balance?.isPending}
                          failed={balance?.isError}
                          detail
                        />
                        <div className="balance-card-footer">
                          <Clock3 size={12} />
                          {balance?.data?.keys?.[0]?.checked_at
                            ? `查询于 ${time(balance.data.keys[0].checked_at)}`
                            : "等待有效余额数据"}
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}
              <div className="table-footer">
                <span>
                  {error
                    ? "数据不可用"
                    : `显示 ${total ? currentPage * 25 + 1 : 0}–${Math.min((currentPage + 1) * 25, total)}，共 ${count(total)} ${channelView ? "个模型 / 渠道组合" : "个渠道"}`}
                  {pendingBalances > 0 && (
                    <span className="footer-pending">
                      <Spinner small />
                      余额查询 {providers.length - pendingBalances}/
                      {providers.length}
                    </span>
                  )}
                </span>
                <div className="pagination">
                  <button
                    className="icon-button"
                    disabled={currentPage === 0}
                    onClick={() => setPage(currentPage - 1)}
                    aria-label="上一页"
                  >
                    <ChevronLeft size={16} />
                  </button>
                  <span>
                    {currentPage + 1}{" "}
                    <span className="muted">/ {pageCount}</span>
                  </span>
                  <button
                    className="icon-button"
                    disabled={currentPage + 1 >= pageCount}
                    onClick={() => setPage(currentPage + 1)}
                    aria-label="下一页"
                  >
                    <ChevronRight size={16} />
                  </button>
                </div>
              </div>
            </section>
          ) : null}
          <AnimatePresence>
            {showTrend && channelView && !error && (
              <Trend
                key={`${keyId}-${window}-${model}-${endpoint}-${stream}`}
                connection={connection}
                keyId={keyId}
                window={window}
                model={model}
                refresh={refresh}
                endpoint={endpoint}
                stream={stream}
              />
            )}
          </AnimatePresence>
        </main>
        <div className="connection-bottom">
          <button onClick={() => disconnect()}>
            <LogOut size={14} />
            {baseConnection.account ? "退出登录" : "断开连接"}
          </button>
        </div>
      </div>
      <Detail
        row={detail}
        site={siteFor(detail) || dashboardURL(sourceList.find((source) => source.id === detail?.source_id)?.base)}
        check={detail ? checks.results.get(providerId(detail)) : undefined}
        trend={{ connection, keyId, window, endpoint, stream, refresh }}
        catalog={catalog.data?.data || []}
        imports={baseConnection.account ? imported : undefined}
        onClose={() => setDetailId(null)}
        balance={detail ? balanceMap.get(providerId(detail))?.data : undefined}
      />
      <Guide open={guide} onClose={() => setGuide(false)} />
    </div>
  );
}

export function LegacyConsole() {
  const [connection, setConnection] = useState<Connection | null>(
    loadConnection,
  );
  const [error, setError] = useState("");
  const [change, setChange] = useState(false);
  const client = useQueryClient();
  const connected = (next: Connection, keys: Keys) => {
    client.clear();
    client.setQueryData(["keys", next.session], keys);
    saveConnection(next);
    setConnection(next);
    setError("");
    setChange(false);
  };
  const disconnect = useCallback(
    (reason = "") => {
      client.clear();
      clearConnection();
      setConnection(null);
      setError(reason);
      setChange(false);
      document.documentElement.dataset.theme = "light";
    },
    [client],
  );
  return (
    <>
      {connection ? (
        <Dashboard
          connection={connection}
          disconnect={disconnect}
          changeConnection={() => setChange(true)}
        />
      ) : (
        <Welcome
          onConnect={() => {}}
          error={error}
          legacyForm={<ConnectionForm onConnect={connected} />}
        />
      )}
      <Dialog.Root open={change} onOpenChange={setChange}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog">
            <Dialog.Title>切换服务连接</Dialog.Title>
            <Dialog.Description>验证成功后切换连接。</Dialog.Description>
            <ConnectionForm
              compact
              initialBase={connection?.base}
              onConnect={connected}
            />
            <Dialog.Close className="button">取消</Dialog.Close>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}

type AccountSession = {
  enabled: boolean;
  authenticated: boolean;
  username: string;
};
export default function App() {
  const client = useQueryClient();
  const [error, setError] = useState("");
  const auth = useQuery({
    queryKey: ["account"],
    queryFn: () => controlRequest<AccountSession>("/v1/auth/me"),
    retry: false,
    staleTime: 30_000,
  });
  const connection = useMemo<Connection>(
    () => ({
      base: globalThis.location.origin,
      key: "",
      session: auth.data?.username || "account",
      account: true,
    }),
    [auth.data?.username],
  );
  const disconnect = useCallback(
    async (reason = "") => {
      try {
        await controlRequest("/v1/auth/logout", { method: "POST", body: "{}" });
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) {
          setError("退出失败，请重试。服务端会话尚未撤销。");
          return;
        }
      }
      await client.cancelQueries();
      client.removeQueries({
        predicate: (query) => query.queryKey[0] !== "account",
      });
      client.setQueryData<AccountSession>(["account"], {
        enabled: true,
        authenticated: false,
        username: "",
      });
      setError(reason);
    },
    [client],
  );
  useEffect(() => {
    if (auth.data?.enabled) clearConnection();
  }, [auth.data?.enabled]);
  if (auth.isPending) return <StartupScreen />;
  if (auth.isError)
    return <StartupScreen onRetry={() => void auth.refetch()} />;
  if (!auth.data.enabled) return <LegacyConsole />;
  if (!auth.data.authenticated)
    return (
      <Welcome
        onConnect={() => {
          setError("");
          void auth.refetch();
        }}
        error={error}
      />
    );
  return (
    <>
      {error && (
        <div role="alert" className="error-banner">
          {error}
          <button onClick={() => void disconnect()}>重试退出</button>
        </div>
      )}
      <Dashboard
        connection={connection}
        disconnect={(reason) => void disconnect(reason)}
        changeConnection={() => {}}
      />
    </>
  );
}
