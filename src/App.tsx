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
  BookOpen,
  Check,
  CheckCheck,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Database,
  Clock3,
  Command,
  ExternalLink,
  Eye,
  EyeOff,
  Filter,
  Gauge,
  Globe2,
  KeyRound,
  Layers3,
  LayoutDashboard,
  Link2,
  LogOut,
  Menu,
  Moon,
  Radio,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Unplug,
  Wallet,
  X,
  TriangleAlert,
} from "lucide-react";
import {
  ApiError,
  channelParams,
  cleanBase,
  makeLimiter,
  request,
  analyticsRequest,
} from "./api";
import { clearConnection, loadConnection, saveConnection } from "./session";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";
import type { Filters } from "./preferences";
import {
  balanceIsLow,
  balanceKind,
  balanceLabel,
  balanceStatus,
  count,
  keyIsLow,
  ms,
  rate,
  reasonLabel,
  rowId,
  summarize,
  time,
} from "./format";
import type {
  Balance,
  Catalog,
  Channel,
  Connection,
  Distribution,
  KeyInfo,
  Metrics,
  ModelPrice,
} from "./types";
import { Brand, Empty, Spinner, Tip } from "./ui";
import { PriceSettings } from "./PriceSettings";
import { catalogMetrics, ranges, usd } from "./analytics";
import { actualCostRange } from "./actualCost";

type Keys = {
  data: KeyInfo[];
  snapshot_revision: string;
  can_inspect_all: boolean;
};
type ConsoleSource = { id: string; name: string; base: string; created_at: number };
type View = "channels" | "balances" | "overview" | "prices";

function Overview({ metrics, rows, live }: { metrics?: Metrics; rows: Channel[]; live?: Map<string, number | null | undefined> }) {
  const total = metrics?.total as Record<string, any> | undefined;
  const models = (metrics as any)?.models || [];
  const currentConcurrency = live ? [...live.values()].reduce<number>((sum, value) => sum + (value || 0), 0) : null;
  const cacheRate = total?.cache_rate ?? (total && total.input_tokens > 0 ? (total.cache_read_tokens || 0) / total.input_tokens : null);
  return <motion.section {...reveal} className="overview-grid">
    <MetricCard label="请求数量" value={count(total?.requests || 0)} sub="所选时间范围" icon={<Activity size={17} />} />
    <MetricCard label="渠道尝试" value={count(total?.attempts || 0)} sub="包含重试与失败尝试" icon={<Radio size={17} />} accent />
    <MetricCard label="Token 数量" value={count((total?.input_tokens || 0) + (total?.output_tokens || 0))} sub="输入 + 输出" icon={<Layers3 size={17} />} />
    <MetricCard label="估算消费" value={total?.estimated_cost_usd == null ? "—" : `$${Number(total.estimated_cost_usd).toFixed(4)}`} sub="依据当前模型价格" icon={<Wallet size={17} />} />
    <MetricCard label="当前并发" value={currentConcurrency == null ? "—" : count(currentConcurrency)} sub="实时渠道请求" icon={<Gauge size={17} />} />
    <MetricCard label="缓存率" value={rate(cacheRate)} sub="缓存读取 / 输入 token" icon={<Database size={17} />} />
    <div className="data-panel overview-panel"><div className="data-title"><Gauge size={18} /><h2>模型消费</h2></div><div className="overview-list">{models.length ? models.map((item: any) => <div className="overview-row" key={item.model}><strong>{item.model}</strong><span>{count((item.input_tokens || 0) + (item.output_tokens || 0))} tokens</span><b>{item.estimated_cost_usd == null ? "—" : `$${Number(item.estimated_cost_usd).toFixed(4)}`}</b></div>) : <Empty title="暂无模型事实" icon={<Activity size={22} />}>等待 S3 事实导入。</Empty>}</div></div>
    <div className="data-panel overview-panel"><div className="data-title"><Radio size={18} /><h2>当前渠道</h2></div><div className="overview-list">{rows.slice(0, 12).map(row => <div className="overview-row" key={rowId(row)}><strong>{row.provider}</strong><span>{row.model}</span><b>{live?.get(rowId(row)) == null ? "—" : `${live.get(rowId(row))} 并发`}</b></div>)}</div></div>
  </motion.section>;
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
const streamLabel = (stream: string) =>
  stream === "true" ? "流式" : stream === "false" ? "非流式" : "全部请求";
async function readMetrics(connection: Connection, path: string, signal: AbortSignal, endpoint: string, stream: string) {
 const source=new URLSearchParams(path.split("?")[1]||"");
 const range=source.get("window")||"15m";
 source.delete("window"); source.set("range",range); source.delete("api_key_id");
 source.set("endpoint",endpoint); source.set("stream",stream);
 if (path.includes("timeseries")) source.set("timeseries","true");
 const result=await analyticsRequest<Metrics>(connection,"/analytics/v1/analytics?"+source.toString(),signal);
 if (!result || !result.total || !Array.isArray(result.data)) throw new Error("分析服务返回了无效统计数据。");
 return {...result,window_minutes: range==="24h"?1440:range==="7d"?10080:range==="30d"?43200:range==="today"?1440:range==="week"?10080:range==="month"?43200:range==="year"?525600:0,coverage:result.coverage||"partial",statistics_scope:"s3"};
}
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

function AccountForm({ onConnect }: { onConnect: (connection: Connection, keys: Keys) => void }) {
  const [username, setUsername] = useState(""), [password, setPassword] = useState("");
  const [pending, setPending] = useState(false), [error, setError] = useState(""), [setup, setSetup] = useState(false), [needsSource, setNeedsSource] = useState(false);
  useEffect(() => { void fetch("/analytics/v1/auth/me", { credentials: "include" }).then(response => response.ok ? response.json() as Promise<{setup_required?: boolean}> : null).then(body => setSetup(Boolean(body?.setup_required))).catch(() => undefined); }, []);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending) return; setPending(true); setError("");
    try {
      const response = await fetch(`/analytics/v1/auth/${setup ? "setup" : "login"}`, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ username, password }) });
      if (!response.ok) throw new Error(response.status === 401 ? "用户名或密码错误。" : setup ? "初始化账户失败，密码至少 12 位。" : "账户服务暂不可用，请确认分析 API 已启用账户模式。");
      const body = await response.json() as { sources?: ConsoleSource[] };
      const source = body.sources?.[0];
      if (!source) { setNeedsSource(true); return; }
      const connection: Connection = { base: window.location.origin, key: "", sourceId: source.id, session: crypto.randomUUID() };
      const keys = await readKeys(connection, new AbortController().signal); onConnect(connection, keys);
    } catch (e) { setError(e instanceof Error ? e.message : "登录失败，请重试。"); } finally { setPending(false); }
  }
  return <form className="connection-form account-form" onSubmit={submit}>
    <label htmlFor="console-username">用户名 <span>ACCOUNT</span></label>
    <div className="input-wrap"><KeyRound size={18} /><input id="console-username" value={username} onChange={e => setUsername(e.target.value)} autoComplete="username" required disabled={pending} /></div>
    <label htmlFor="console-password">密码 <span>CONSOLE PASSWORD</span></label>
    <div className="input-wrap"><ShieldCheck size={18} /><input id="console-password" type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="current-password" required disabled={pending} /></div>
    {error && <div role="alert" className="error-banner"><TriangleAlert size={17} /><span>{error}</span></div>}
    <button className="button primary connect-button" disabled={pending}>{pending ? <><Spinner small /> {setup ? "正在初始化" : "正在登录"}</> : <>{setup ? "创建管理员账户" : "账户登录"} <ArrowRight size={17} /></>}</button>
    <button type="button" className="text-link account-mode" onClick={() => setSetup(!setup)}>{setup ? "已有账户？登录" : "首次使用？创建账户"}</button>
    {needsSource && <SourceForm onAdded={async () => { const body = await fetch("/analytics/v1/sources", { credentials: "include" }).then(response => response.json()) as {data?: ConsoleSource[]}; const source = body.data?.[0]; if (source) onConnect({ base: window.location.origin, key: "", sourceId: source.id, session: crypto.randomUUID() }, await readKeys({ base: window.location.origin, key: "", sourceId: source.id, session: crypto.randomUUID() }, new AbortController().signal)); }} />}
    <p className="connection-help">登录后可管理多个 uni-api 来源，来源密钥只保存在分析 API。</p>
  </form>;
}

function SourceForm({ onAdded }: { onAdded: () => void }) {
  const [name,setName]=useState(""),[base,setBase]=useState(""),[key,setKey]=useState(""),[error,setError]=useState(""),[pending,setPending]=useState(false);
  async function submit(event: FormEvent) { event.preventDefault(); setPending(true); setError(""); try { const response=await fetch("/analytics/v1/sources",{method:"POST",credentials:"include",headers:{"Content-Type":"application/json","Accept":"application/json"},body:JSON.stringify({name,base,key})}); if(!response.ok) throw new Error("添加来源失败，请检查地址和平台密钥。"); setName("");setBase("");setKey("");onAdded(); } catch(e){setError(e instanceof Error?e.message:"添加失败")} finally{setPending(false)} }
  return <form className="source-form" onSubmit={submit}><h3>添加 uni-api 来源</h3><input aria-label="来源名称" placeholder="来源名称，例如 DigitalOcean" value={name} onChange={e=>setName(e.target.value)} required/><input aria-label="来源地址" type="url" placeholder="https://uni-api.example.com" value={base} onChange={e=>setBase(e.target.value)} required/><input aria-label="来源平台密钥" type="password" placeholder="第一个平台密钥" value={key} onChange={e=>setKey(e.target.value)} required minLength={8}/>{error&&<div role="alert" className="error-banner"><TriangleAlert size={16}/>{error}</div>}<button className="button primary" disabled={pending}>{pending?<><Spinner small/> 保存中</>:<>保存来源 <Check size={16}/></>}</button></form>;
}
const reveal = {
  initial: { opacity: 0, y: 12 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.32 },
};

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

function Welcome({
  onConnect,
  error,
}: {
  onConnect: (connection: Connection, keys: Keys) => void;
  error: string;
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
              {["首输出延迟", "请求前等待", "渠道余额"].map((label, i) => (
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
              直接连接你的服务
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
          <AccountForm onConnect={onConnect} />
          <details className="legacy-connect"><summary>兼容：直接连接单个 uni-api</summary>
          <ConnectionForm onConnect={onConnect} initialError={error} />
          </details>
          <div className="connect-card-footer">
            <span className="tiny-dot" /> 浏览器直连 · 无需额外账户
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

function BalanceValue({
  balance,
  loading,
  failed,
  detail = false,
}: {
  balance?: Balance;
  loading?: boolean;
  failed?: boolean;
  detail?: boolean;
}) {
  if (loading && !balance)
    return (
      <span className="loading-text">
        <Spinner small />
        查询中
      </span>
    );
  if (!balance || failed) return <span className="muted">查询失败</span>;
  if (!balance.keys?.length)
    return (
      <span className="muted">
        {balanceStatus[balance.status] || "暂无数据"}
      </span>
    );
  return (
    <div className={`balance-values ${detail ? "expanded" : ""}`}>
      {balance.keys.map((item) => (
        <Tip
          key={item.position}
          text={
            <>
              <strong>{balanceKind[item.kind || ""] || "余额查询"}</strong>
              <br />
              {item.checked_at
                ? `查询时间 ${time(item.checked_at)} · 最多缓存 5 分钟`
                : "上游尚未提供金额"}
              <br />
              同一账户的多个渠道余额可能共享，不可相加。
            </>
          }
        >
          <span className={keyIsLow(item) ? "amount negative" : "amount"}>
            {balance.keys!.length > 1 && <small>Key {item.position} </small>}
            {balanceLabel(item)}
            {detail && (
              <small className="balance-kind">
                {balanceKind[item.kind || ""]}
              </small>
            )}
          </span>
        </Tip>
      ))}
      {!!balance.omitted_keys && (
        <small>另 {balance.omitted_keys} 个密钥未查询</small>
      )}
    </div>
  );
}
function Timing({
  value,
  wait = false,
}: {
  value?: Distribution;
  wait?: boolean;
}) {
  return (
    <Tip
      text={
        <>
          <strong>
            {wait
              ? "请求进入 uni-api → 渠道 HTTP 发起前"
              : "渠道请求发起 → 首次语义输出"}
          </strong>
          <br />
          p95 {ms(value?.p95_ms)} · 最近 {ms(value?.last_ms)}
          <br />
          {count(value?.sample_count || 0)} 次样本 · 分位值为直方图上界估计
          {wait && (
            <>
              <br />
              包含读包、排队与前序重试；不包含入口前耗时。
            </>
          )}
        </>
      }
    >
      <span className={`metric-value ${value?.p50_ms == null ? "muted" : ""}`}>
        {ms(value?.p50_ms)}
      </span>
    </Tip>
  );
}
function Status({ row }: { row: Channel }) {
  return (
    <span className={`status-pill ${row.eligible ? "healthy" : "cooling"}`}>
      <span className="tiny-dot" />
      {reasonLabel[row.reason] || (row.eligible ? "可用" : "不可用")}
    </span>
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
    return sorted.filter((_, index) => index % stride === 0).slice(0, maxPoints);
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
}: {
  row: Channel | null;
  onClose: () => void;
  balance?: Balance;
}) {
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
          <Dialog.Title>{row?.provider}</Dialog.Title>
          <Dialog.Description className="detail-description">
            {row?.model} <ArrowRight size={13} /> {row?.upstream_model}
          </Dialog.Description>
          {row && (
            <>
              <Status row={row} />
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
                    <small>收到首输出</small>
                    <strong>+ {ms(row.stats?.first_output?.p50_ms)}</strong>
                    <span>渠道首输出 p50</span>
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
                    <dt>首输出 p95</dt>
                    <dd>{ms(row.stats?.first_output?.p95_ms)}</dd>
                  </div>
                  <div>
                    <dt>最近一次首输出</dt>
                    <dd>{ms(row.stats?.first_output?.last_ms)}</dd>
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
                "首输出延迟",
                "当前渠道请求开始到首次语义输出的耗时。与请求前等待分别聚合，分位值不可直接相加。p50 / p95 是直方图上界估计。",
              ],
              [
                "余额不足",
                "余额或额度 ≤ 0；多密钥渠道须全部已查询且均不足。未适配、失败和未知不算不足，账户共享余额不可相加。",
              ],
              [
                "范围与保留",
                "默认统计全部端点和全部流式状态，可分别筛选。跨组延迟由后端合并直方图后计算。默认按 provider 配置顺序；选择 API key 后按该 key 配置顺序，仍是渠道整体统计而非 key 私有用量。指标在内存保留 1 小时，实例重启后重新积累。",
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
  const [sourceList, setSourceList] = useState<ConsoleSource[]>([]);
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const connection = useMemo(() => ({ ...baseConnection, sourceId: selectedSourceId || undefined }), [baseConnection, selectedSourceId]);
  const [filters, setFilters] = useState(() => loadFilters(baseConnection.base));
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
  const [view, setView] = useState<View>("channels"),
    [page, setPage] = useState(0);
  useEffect(() => {
    saveFilters(connection.base, filters);
  }, [connection.base, filters]);
  const hasFilters = (Object.keys(defaultFilters) as (keyof Filters)[]).some(
    (key) => filters[key] !== defaultFilters[key],
  );
  const [detailId, setDetailId] = useState<string | null>(null),
    [showTrend, setShowTrend] = useState(false),
    [guide, setGuide] = useState(false),
    [menu, setMenu] = useState(false),
    [refresh, setRefresh] = useState(0);
  useEffect(() => { if (!baseConnection.sourceId) return; void analyticsRequest<{data: ConsoleSource[]}>(baseConnection, "/analytics/v1/sources").then(body => setSourceList(body.data || [])).catch(() => setSourceList([])); }, [baseConnection]);
  const [auto, setAuto] = useState(false),
    [theme, setTheme] = useState(() => {
      try {
        return localStorage.getItem("uni-console-theme") || "light";
      } catch {
        return "light";
      }
    });
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("uni-console-theme", theme);
    } catch {
      /* theme persistence is optional */
    }
  }, [theme]);
  const deferredSearch = useDeferredValue(search);
  const keys = useQuery({
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
    enabled: keysLoaded && !keyRemoved,
  });
  const prices = useQuery({
    queryKey: ["prices", connection.session],
    queryFn: ({ signal }) => analyticsRequest<{ data: ModelPrice[] }>(connection, "/analytics/v1/prices", signal),
    enabled: keysLoaded && view === "prices",
    staleTime: 60_000,
  });
  const metrics = useQuery({
    queryKey: ["metrics", connection.session, keyId, window, endpoint, stream],
    queryFn: ({ signal }) => readMetrics(connection,"/v1/channel-metrics?" + params,signal,endpoint,stream),
    staleTime: 30_000,
    enabled: keysLoaded && !keyRemoved && !!catalog.data,
    refetchInterval: auto ? 30_000 : false,
    refetchIntervalInBackground: false,
  });
  const liveMetrics = useQuery({
    queryKey: ["live-metrics", connection.session, keyId, endpoint, stream],
    queryFn: ({ signal }) => request<Metrics>(connection, "/v1/channel-metrics?" + channelParams(keyId, "1m", "", endpoint, stream), signal),
    enabled: keysLoaded && !keyRemoved && !!catalog.data && (view === "overview" || view === "channels"),
    staleTime: 2_000,
    refetchInterval: auto ? 5_000 : false,
    refetchIntervalInBackground: false,
  });
  const liveMap = useMemo(() => new Map((liveMetrics.data?.data || []).map(row => [rowId(row), row.stats?.inflight])), [liveMetrics.data]);
  const queryClient = useQueryClient();
  useEffect(() => {
    if (!keysLoaded || keyRemoved || !catalog.data || view !== "channels") return;
    const windows = ranges.map(([value]) => value);
    const controller = new AbortController();
    void (async () => {
      // Prefetch ranges concurrently. Sequential prefetch made the last
      // windows wait behind every earlier range, so switching to month/year/all
      // could miss the one-second interaction target even though each DuckDB
      // query itself was fast.
      await Promise.all(windows.filter((next) => next !== window).map(async (next) => {
        if (controller.signal.aborted) return;
        const key = ["metrics", connection.session, keyId, next, endpoint, stream];
        if (queryClient.getQueryData(key)) return;
        await queryClient.prefetchQuery({
          queryKey: key,
          queryFn: ({ signal }) => readMetrics(connection, "/v1/channel-metrics?" + channelParams(keyId, next, "", endpoint, stream), signal, endpoint, stream),
          staleTime: 30_000,
        });
      }));
    })();
    return () => controller.abort();
  }, [keysLoaded, keyRemoved, catalog.data, view, connection, keyId, endpoint, stream, window, queryClient]);
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
  const error = keyRemoved
    ? "所选 API key 已移除，请重新选择。"
    : modelRemoved
      ? "当前 API key 未配置所选模型，请重新选择模型。"
      : keys.error?.message || metrics.error?.message || catalog.error?.message;
  const rows = useMemo(
    () =>
      error
        ? []
        : catalogMetrics(catalog.data, metrics.data).filter(
            (row) => !model || row.model === model,
          ),
    [catalog.data, metrics.data, model, error],
  );
  const rowRanks = useMemo(
    () => new Map(rows.map((row, i) => [rowId(row), i + 1])),
    [rows],
  );
  const providers = useMemo(
    () => [...new Set(rows.map((row) => row.provider))],
    [rows],
  );
  const actualRange = useMemo(() => actualCostRange(window), [window]);
  const limit = useMemo(() => makeLimiter(3), []);
  const balanceQueries = useQueries({
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
              connection,
              "/v1/channel-balances?" + new URLSearchParams({
                provider,
                ...(model ? { model } : {}),
                ...(actualRange.startDate ? { start_date: actualRange.startDate } : {}),
                ...(actualRange.endDate ? { end_date: actualRange.endDate } : {}),
              }),
              signal,
            ),
          signal,
        ),
      staleTime: 300_000,
      refetchInterval: auto ? 300_000 : false,
      refetchIntervalInBackground: false,
      retry: false,
    })),
  });
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
  const filtered = rows.filter(
    (row) =>
      (!deferredSearch ||
        `${row.provider} ${row.model} ${row.upstream_model}`
          .toLowerCase()
          .includes(deferredSearch.toLowerCase())) &&
      (!balanceFilter || balanceIsLow(balanceMap.get(row.provider)?.data)) &&
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
            ? row.stats?.first_output?.p50_ms
            : row.stats?.request_to_dispatch?.p50_ms;
      return (values(a) ?? Infinity) - (values(b) ?? Infinity);
    });
  const balanceProviders = [...new Set(filtered.map((row) => row.provider))];
  const total = view === "channels" ? filtered.length : balanceProviders.length;
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
    void keys.refetch();
    void liveMetrics.refetch();
    if (keysLoaded && !keyRemoved) {
      void catalog.refetch();
      void metrics.refetch();
    }
    for (const query of balanceQueries) void query.refetch();
    setRefresh((x) => x + 1);
  }
  function selectView(next: View) {
    setView(next);
    setPage(0);
    setMenu(false);
  }
  const nav = (
    <>
      <Brand />
      <div className="workspace-label">WORKSPACE</div>
      <nav aria-label="主导航">
        <button className={view === "overview" ? "active" : ""} onClick={() => selectView("overview")}>
          <Gauge size={18} /> 总览
        </button>
        <button
          className={view === "channels" ? "active" : ""}
          onClick={() => selectView("channels")}
        >
          <LayoutDashboard size={18} />
          渠道观测<span className="nav-shortcut">⌘ 1</span>
        </button>
        <button className={view === "prices" ? "active" : ""} onClick={() => selectView("prices")}>
          <SlidersHorizontal size={18} /> 价格设置
        </button>
        <button
          className={view === "balances" ? "active" : ""}
          onClick={() => selectView("balances")}
        >
          <Wallet size={18} />
          余额管理
          {lowCount > 0 && <span className="nav-count">{lowCount}</span>}
        </button>
      </nav>
      <div className="sidebar-insight">
        <div className="insight-icon">
          <Radio size={20} />
        </div>
        <h3>把复杂，留给路由。</h3>
        <p>把清晰，留给你。</p>
        <div className="signal-bars">
          {[
            7, 13, 9, 20, 15, 29, 22, 34, 18, 27, 36, 23, 30, 17, 28, 38, 25,
            33,
          ].map((height, i) => (
            <i key={i} style={{ height }} />
          ))}
        </div>
      </div>
      <div className="sidebar-bottom">
        <button
          onClick={() => {
            setGuide(true);
            setMenu(false);
          }}
        >
          <CircleHelp size={18} />
          指标说明
          <ArrowUpRight size={15} />
        </button>
        <a
          href="https://github.com/yym68686/uni-api-web"
          target="_blank"
          rel="noreferrer"
        >
          <BookOpen size={18} />
          开源项目
          <ExternalLink size={14} />
        </a>
        <div className="sidebar-version">
          <span className="tiny-dot" /> uni-api console <small>2.0</small>
        </div>
      </div>
    </>
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
        <header className="topbar">
          <div className="breadcrumb">
            <button
              className="icon-button menu-toggle"
              onClick={() => setMenu(true)}
              aria-label="打开菜单"
            >
              <Menu size={20} />
            </button>
            <span>工作空间</span>
            <ChevronRight size={13} />
            <strong>{view === "channels" ? "渠道观测" : view === "balances" ? "余额管理" : view === "overview" ? "总览" : "价格设置"}</strong>
          </div>
          <div className="topbar-actions">
            {sourceList.length > 0 && <label className="source-switcher"><Server size={14} /><select aria-label="uni-api 来源" value={selectedSourceId} onChange={e => { setSelectedSourceId(e.target.value); setFilter("keyId", ""); }}><option value="">全部来源</option>{sourceList.map(source => <option value={source.id} key={source.id}>{source.name}</option>)}</select></label>}
            <span className="topbar-service">
              <span className="tiny-dot" />
              {new URL(connection.base).hostname}
            </span>
            <button
              className="icon-button"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              aria-label={theme === "dark" ? "切换浅色模式" : "切换深色模式"}
            >
              {theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
            </button>
            <button
              className="avatar"
              onClick={changeConnection}
              aria-label="管理服务连接"
            >
              U
            </button>
          </div>
        </header>
        <main className="workspace">
          <motion.div {...reveal} className="page-heading">
            <div>
              <span className="eyebrow">OBSERVE. UNDERSTAND. OPTIMIZE.</span>
              <h1>
                {view === "channels"
                  ? "每条渠道，尽在视野。"
                  : view === "balances" ? "余额有数，调用有底。" : view === "overview" ? "全局请求，一眼掌握。" : "模型价格，按你的口径计算。"}
              </h1>
              <p>
                {view === "channels"
                  ? "从可用性到首输出，了解模型请求的每一步。"
                  : view === "balances" ? "独立查看每个渠道的上游余额与额度。" : view === "overview" ? "消费、请求、token 与缓存率来自 S3 事实聚合。" : "价格按每百万 token 计，保存后用于后续估算。"}
              </p>
            </div>
            <button
              className={`button ${busy ? "refreshing" : ""}`}
              onClick={reload}
              disabled={busy}
            >
              <RefreshCw size={16} className={busy ? "spin" : ""} />
              刷新数据
            </button>
          </motion.div>
          {view === "prices" ? (
            <><SourceForm onAdded={() => void analyticsRequest<{data: ConsoleSource[]}>(connection, "/analytics/v1/sources").then(body => setSourceList(body.data || []))} /><PriceSettings prices={prices.data?.data || []} loading={prices.isPending} error={prices.error?.message} connection={connection} onSaved={() => void prices.refetch()} /></>
          ) : view === "overview" ? (
            <Overview metrics={metrics.data} rows={rows} live={liveMap} />
          ) : <motion.section
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
          </motion.section>}
          {view === "channels" || view === "balances" ? <section className="data-panel">
            <div className="data-heading">
              <div className="data-title">
                <span className="section-icon">
                  {view === "channels" ? (
                    <Activity size={19} />
                  ) : (
                    <Wallet size={19} />
                  )}
                </span>
                <h2>{view === "channels" ? "渠道表现" : "渠道余额"}</h2>
                <span className="count-badge">{count(total)}</span>
              </div>
              <div className="data-actions">
                {view === "channels" && (
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
                      Key {key.position} · {key.prefix}
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
              <div className="window-tabs" aria-label="统计窗口">
                {ranges.map(([value, label]) => (
                  <button
                    key={value}
                    aria-pressed={window === value}
                    className={window === value ? "active" : ""}
                    onClick={() => setFilter("window", value)}
                  >
                    {window === value && (
                      <motion.span
                        className="window-highlight"
                        layoutId="window-tab"
                        transition={{
                          type: "spring",
                          stiffness: 500,
                          damping: 38,
                        }}
                      />
                    )}
                    <span>{label}</span>
                  </button>
                ))}
              </div>
            </div>
            <div className="filter-secondary">
              <div className="filter-chips">
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
                    <option value="latency">首输出从低到高</option>
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
              <span className="scope-label">
                <span className="tiny-dot" />{" "}
                {endpoint === "all" ? "全部端点" : endpoint} ·{" "}
                {streamLabel(stream)}{" "}
                <Tip text="统计所选端点与流式范围内的渠道整体尝试。API key 筛选决定渠道集合与顺序，不是该 key 的独立用量。全部端点的可用状态表示渠道整体冷却和凭据状态。">
                  <CircleHelp size={13} />
                </Tip>
              </span>
            </div>
            {metrics.data?.import && !metrics.data.import.caught_up && <div className="coverage-note" role="status"><Clock3 size={14} />{metrics.data.import.error_class ? `采集异常：${metrics.data.import.error_class}` : `正在同步历史事实，剩余 ${count(metrics.data.import.remaining_objects || 0)} 个对象；当前统计尚不完整。`}</div>}
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
            ) : metrics.isPending ? (
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
            ) : view === "channels" ? (
              <div className="table-scroll">
                <table className="channel-table">
                  <thead>
                    <tr>
                      <th className="rank">#</th>
                      <th>渠道 / 模型</th>
                      <th>状态</th>
                      <th>当前并发</th>
                      <th>
                        <Tip text="成功与失败的渠道尝试分别计数，重试不是新的用户请求。">
                          成功率 <CircleHelp size={12} />
                        </Tip>
                      </th>
                      <th>尝试数</th>
                      <th>
                        <Tip text="请求进入 uni-api → 渠道 HTTP 发起前。包含前序重试耗时，悬停或展开查看详情。">
                          请求前等待 <small>p50</small>
                        </Tip>
                      </th>
                      <th>
                        首输出 <small>p50 / p95</small>
                      </th>
                      <th>Token / 缓存率</th>
                      <th>估算消费</th>
                      <th>
                        <Tip text="来自上游 sub2api 的 actual_cost，按日历日统计；充值增加不会计入消费。5 分钟、15 分钟和 1 小时窗口没有可验证的上游小时账单。">
                          实际消费 <CircleHelp size={12} />
                        </Tip>
                      </th>
                      <th>余额 / 额度</th>
                      <th aria-label="详情" />
                    </tr>
                  </thead>
                  <tbody>
                    {pageRows.map((row) => {
                      const balance = balanceMap.get(row.provider);
                      const success = row.stats?.success_rate;
                      return (
                        <tr key={rowId(row)}>
                          <td className="rank mono">
                            {String(rowRanks.get(rowId(row))).padStart(2, "0")}
                          </td>
                          <td>
                            <button
                              className="channel-link"
                              onClick={() => setDetailId(rowId(row))}
                            >
                              <span className="provider-avatar">
                                {row.provider.slice(0, 1).toUpperCase()}
                              </span>
                              <span>
                                <strong>{row.provider}</strong>
                                <small>{row.model}</small>
                              </span>
                            </button>
                          </td>
                          <td>
                            <Status row={row} />
                          </td>
                          <td className="mono">
                            {liveMap.get(rowId(row)) == null ? "—" : liveMap.get(rowId(row))}
                          </td>
                          <td>
                            <div className="success-cell">
                              <span
                                className={`mono ${success == null ? "muted" : success < 0.5 ? "negative" : ""}`}
                              >
                                {rate(success)}
                              </span>
                              <span className="rate-track">
                                <i
                                  className={
                                    success != null && success < 0.5
                                      ? "low"
                                      : ""
                                  }
                                  style={{ width: `${(success || 0) * 100}%` }}
                                />
                              </span>
                            </div>
                          </td>
                          <td className="mono">
                            {count(row.stats?.success_rate_denominator || 0)}
                          </td>
                          <td>
                            <Timing
                              value={row.stats?.request_to_dispatch}
                              wait
                            />
                          </td>
                          <td>
                            <div className="dual-metric">
                              <Timing value={row.stats?.first_output} />
                              <span className="muted mono">
                                {ms(row.stats?.first_output?.p95_ms)}
                              </span>
                            </div>
                          </td>
                          <td className="mono">{row.stats?.usage_samples ? count((row.stats.input_tokens || 0) + (row.stats.output_tokens || 0)) : "—"}<small className="usage-cache">{rate(row.stats?.cache_rate)}</small></td>
                          <td className="mono">{usd(row.stats?.estimated_cost_usd)}</td>
                          <td className="mono">
                            {!actualRange.supported
                              ? <Tip text="sub2api 只提供按日聚合的 actual_cost，当前滚动窗口不显示整日金额。"><span className="muted">按日</span></Tip>
                              : balance?.isPending && !balance.data
                                ? <span className="muted">查询中</span>
                                : balance?.data?.actual_cost_usd == null
                                  ? <span className="muted">—</span>
                                  : usd(balance.data.actual_cost_usd)}
                          </td>
                          <td>
                            <BalanceValue
                              balance={balance?.data}
                              loading={balance?.isPending}
                              failed={balance?.isError}
                            />
                          </td>
                          <td>
                            <button
                              className="row-arrow icon-button"
                              onClick={() => setDetailId(rowId(row))}
                              aria-label={`查看 ${row.provider} ${row.model} 详情`}
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
                  return (
                    <article
                      className={`balance-card ${balanceIsLow(balance?.data) ? "low-balance" : ""}`}
                      key={provider}
                    >
                      <div className="balance-card-top">
                        <span className="provider-avatar">
                          {provider[0].toUpperCase()}
                        </span>
                        <span>
                          <strong>{provider}</strong>
                          <small>
                            {
                              rows.filter((row) => row.provider === provider)
                                .length
                            }{" "}
                            个模型
                          </small>
                        </span>
                        {balanceIsLow(balance?.data) && (
                          <span className="status-pill cooling">余额不足</span>
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
                  : `显示 ${total ? currentPage * 25 + 1 : 0}–${Math.min((currentPage + 1) * 25, total)}，共 ${count(total)} ${view === "channels" ? "个模型 / 渠道组合" : "个渠道"}`}
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
                  {currentPage + 1} <span className="muted">/ {pageCount}</span>
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
          </section> : null}
          <AnimatePresence>
            {showTrend && view === "channels" && !error && (
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
          <footer className="workspace-footer">
            <span>
              <ShieldCheck size={13} />
              只读观测 · 当前标签页会话
            </span>
            <button onClick={() => setGuide(true)}>
              了解统计口径 <ArrowUpRight size={13} />
            </button>
            <span className="last-update">
              {busy
                ? "正在同步…"
                : metrics.data
                  ? `数据更新于 ${time(metrics.data.generated_at)}`
                  : "尚未取得数据"}
            </span>
          </footer>
        </main>
        <div className="connection-bottom">
          <span>
            <Server size={14} />
            {connection.base}
          </span>
          <button onClick={() => disconnect()}>
            <LogOut size={14} />
            断开连接
          </button>
        </div>
      </div>
      <Detail
        row={detail}
        onClose={() => setDetailId(null)}
        balance={detail ? balanceMap.get(detail.provider)?.data : undefined}
      />
      <Guide open={guide} onClose={() => setGuide(false)} />
    </div>
  );
}

export default function App() {
  const [connection, setConnection] = useState<Connection | null>(
      loadConnection,
    ),
    [change, setChange] = useState(false),
    [connectionError, setConnectionError] = useState("");
  const client = useQueryClient();
  function connected(next: Connection, keys: Keys) {
    void client.cancelQueries();
    client.clear();
    client.setQueryData(["keys", next.session], keys);
    saveConnection(next);
    setConnection(next);
    setChange(false);
    setConnectionError("");
  }
  const disconnect = useCallback(
    (reason = "") => {
      void client.cancelQueries();
      client.clear();
      setConnection(null);
      clearConnection();
      setChange(false);
      setConnectionError(reason);
      document.documentElement.dataset.theme = "light";
    },
    [client],
  );
  return (
    <>
      <AnimatePresence mode="wait">
        {connection ? (
          <motion.div
            key={connection.session}
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.2 }}
          >
            <Dashboard
              connection={connection}
              disconnect={disconnect}
              changeConnection={() => setChange(true)}
            />
          </motion.div>
        ) : (
          <Welcome
            key="welcome"
            onConnect={connected}
            error={connectionError}
          />
        )}
      </AnimatePresence>
      <Dialog.Root open={change} onOpenChange={setChange}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog">
            <Dialog.Close
              className="icon-button detail-close"
              aria-label="关闭连接设置"
            >
              <X size={20} />
            </Dialog.Close>
            <Dialog.Title>切换服务连接</Dialog.Title>
            <Dialog.Description>
              验证成功后才会切换，原连接会话随之清除。
            </Dialog.Description>
            <ConnectionForm
              compact
              initialBase={connection?.base}
              onConnect={connected}
            />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
