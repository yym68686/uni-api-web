import { channelLatency, latencyLabel } from "./channelLatency";
import type { ManagementPagePosition } from "./managementQuery";
import { restoredDashboardAt, saveDashboardSnapshot, dashboardSnapshotKey } from "./dashboardSnapshot";
import { PageLoading, TableLoading, DetailLoading } from "./PageLoading";
import { endpointChoices, readKeys } from "./requestFilters";
import { FailureCounts, failureReasonLabel } from "./FailureCounts";
import { BalanceAmount } from "./BalanceAmount";
import { useKeyRequestStats } from "./keyRequestStats";
import {
  Fragment,
  lazy,
  Suspense,
  useDeferredValue,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ReactNode } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, m as motion } from "motion/react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  Activity,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Database,
  Clock3,
  Filter,
  Gauge,
  GripVertical,
  KeyRound,
  Layers3,
  Radio,
  RefreshCw,
  Search,
  Server,
  SlidersHorizontal,
  Unplug,
  Wallet,
  X,
} from "lucide-react";
import {
  ApiError,
  AnalyticsInitializingError,
  initializationRetryInterval,
  channelParams,
  request,
  analyticsRequest,
} from "./api";
import { ObservedDetectionActions, ObservedDetectionRow, useObservedDetection } from "./ObservedDetection";
import {
  ChannelCheckAction,
  LatestChannelCheck,
  useChannelChecks,
  CheckVerdict,
  withSubQuality,
  useSubQualitySummary,
} from "./ChannelChecks";
import {
  useChannelControls,
  ChannelControlReset,
  ChannelRowActions,
} from "./ChannelControls";
import { useChannelRouteDrag } from "./ChannelRouteDrag";
import { ResponseLatency } from "./LatencyBadge";
import { useSubImports, boundGroups } from "./sub2apiImports";
import type { SubImportsQuery } from "./sub2apiImports";
import { ChannelModels } from "./ChannelModels";
import { DetailDrawer } from "./DetailDrawer";
import { ChannelSortDialog } from "./ChannelSortDialog";
import { previewChannelOrder, sortChannels } from "./channelSorting";
import { applyScopedChannelSort, channelSortScope, deleteSortTemplate, loadChannelSortPreferences, saveChannelSortPreferences, scopedChannelSort } from "./channelSortPreferences";
import { useRouteSorting } from "./routeSorting";

import { SiteLink, useChannelSites, dashboardURL } from "./ChannelSite";
import { ChannelAccess } from "./ChannelAccess";
import { ChannelSettings } from "./ChannelSettings";
import { CacheTrend } from "./CacheTrend";
import { ChannelDetailTrends } from "./ChannelDetailTrends";
import { ChannelDetailChecks } from "./ChannelDetailChecks";
import { FingerprintCell, fingerprintNote, observedFingerprints, useModelFingerprints } from "./ModelFingerprints";
import type { CacheTrendProps } from "./CacheTrend";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import type { ChannelCheck } from "./ChannelChecks";
import { channelName } from "./format";
import { channelBilling, channelBillingHint } from "./channelBilling";
import { useConsoleSources } from "./consoleSources";
import {
  defaultFilters,
  loadFilters,
  saveFilters,
  loadView,
  saveView,
} from "./preferences";
import type { Filters, View } from "./preferences";
import {
  balanceIsLow,
  balanceIsKnown,
  hasWalletAmount,
  walletStatusLabel,
  count,
  dateTime,
  ms,
  rate,
  rowId,
  providerId,
} from "./format";
import type {
  Catalog,
  Channel,
  Connection,
  Metrics,
  ModelPrice,
} from "./types";
import { Empty, Spinner, Tip } from "./ui";
import { catalogMetrics, ranges, staleHistorySources, usd } from "./analytics";
import { readMetrics } from "./metricsApi";
import { trafficBuckets } from "./trafficBuckets";
import {
  BalanceValue,
  Status,
  ChannelMetricHeaders,
  ChannelMetricCells,
} from "./ChannelMetrics";
import { observationColumnClass, observationColumns, observationPreset, type ChannelMetricLayout } from "./observationColumns";
import { TableColumns, useTableColumns } from "./TableColumns";
import { actualCostRange } from "./actualCost";
import { useScopedChannelSpend } from "./SubChannelSpend";
import { balanceSpend } from "./balanceSpend";
import { useChannelAccountBalances, walletGroups, type SiteBalanceResult } from "./ChannelAccountBalances";
import { balanceSite, scheduleBalance } from "./balanceRequests";
import { balanceThresholdState, rankBalanceProviders } from "./balanceFilters";
import { useBalanceModelMetrics, balanceModelMetricID, BalanceModelMetricCells } from "./BalanceModelMetrics";
import { salePercent } from "./modelPrices";
import { ConsoleHeader, ConsoleLocator, ConsoleNavigation } from "./ConsoleChrome";
import { useCompactWorkbench, useWorkbenchTools, WorkbenchDisclosure, WorkbenchNotice, WorkbenchTabs } from "./Workbench";
import { PageBoundary } from "./PageBoundary";
import type { PriceDrafts } from "./PriceSettings";
import { hasChannelScope, readWorkspace, requestLocation, validLogWindow, workspaceHash } from "./workspaceLocation";
import { useTheme } from "./theme";
import { loadManagementModule } from "./workspaceModules";
import "./dashboard.css";
import "./channelDetail.css";
const SourceSettings = lazy(() => import("./SourceSettings").then(m => ({default:m.SourceSettings})));
const Sub2apiChecks = lazy(() => loadManagementModule().then(m => ({default:m.Sub2apiChecks})));
const RequestLogsPage = lazy(() => import("./RequestLogs").then(m => ({default:m.RequestLogsPage})));
const PriceSettings = lazy(() => import("./PriceSettings").then(m => ({default:m.PriceSettings})));
const Automation = lazy(() => import("./Automation").then(m => ({default:m.Automation})));
const BalanceWalletDialog = lazy(() => import("./BalanceWalletDialog"));


function Overview({
  metrics,
  rows,
  live,
  liveError,
  livePending,
  catalogReady,
  historyError,
  onLogs,
  onChannel,
}: {
  metrics?: Metrics;
  rows: Channel[];
  live: Map<string, number | null | undefined>;
  liveError: boolean;
  livePending: boolean;
  catalogReady: boolean;
  historyError?: string;
  onLogs: (model?: string, failed?: boolean) => void;
  onChannel: (row: Channel) => void;
}) {
  const total = metrics?.total;
  const models = (metrics?.models || []).filter((item): item is {
    model: string; estimated_cost_usd?: number; input_tokens?: number; output_tokens?: number;
  } => typeof item.model === "string" && !!item.model);
  const knownLive = [...live.values()].filter(
    (value): value is number => typeof value === "number" && Number.isFinite(value) && value >= 0,
  );
  const liveComplete = !liveError && !livePending && catalogReady &&
    knownLive.length === live.size && (rows.length === 0 || knownLive.length > 0) &&
    rows.every(row => typeof live.get(rowId(row)) === "number");
  const currentConcurrency = liveComplete ? knownLive.reduce((sum, value) => sum + value, 0) : null;
  const number = (value: unknown) =>
    typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
  const countKnown = (value: unknown) => { const n = number(value); return n == null ? "—" : count(n); };
  const tokenCount = (values: {input_tokens?: unknown;output_tokens?: unknown} | undefined) => {
    const input = number(values?.input_tokens), output = number(values?.output_tokens);
    if (input == null && output == null) return "—";
    return count((input ?? 0) + (output ?? 0));
  };
  const tokenLabel = (values: Parameters<typeof tokenCount>[0]) => {
    const input = number(values?.input_tokens), output = number(values?.output_tokens);
    return input == null && output == null ? "暂无 token 数据" : input == null ? "仅输出 token" : output == null ? "仅输入 token" : "输入与输出 token";
  };
  const money = (value: unknown) => number(value) == null ? "—" : `$${Number(value).toFixed(4)}`;
  const cacheRate =
    number(total?.cache_rate) ??
    (number(total?.input_tokens) && number(total?.cache_read_tokens) != null
      ? total!.cache_read_tokens / total!.input_tokens
      : null);
  const priorityRows = [...rows].sort((a,b) =>
    Number(a.eligible) - Number(b.eligible) ||
    (live.get(rowId(b)) ?? -1) - (live.get(rowId(a)) ?? -1) ||
    (b.stats?.failed || 0) - (a.stats?.failed || 0));
  const offers = new Map<string, Channel>();
  for (const row of metrics?.data || []) {
    const cost = number(row.stats?.estimated_cost_usd);
    if (cost == null) continue;
    const existing = offers.get(row.model);
    if (!existing || cost > (existing.stats.estimated_cost_usd ?? -1)) offers.set(row.model, row);
  }
  const knownModelCost = models.reduce((sum, item) => sum + (number(item.estimated_cost_usd) ?? 0), 0);
  const rankedModels = [...models].sort((a, b) =>
    (number(b.estimated_cost_usd) ?? -1) - (number(a.estimated_cost_usd) ?? -1) || a.model.localeCompare(b.model));
  const [showAllModels, setShowAllModels] = useState(false);
  const [showAllChannels, setShowAllChannels] = useState(false);
  const previewCount = 8;
  return (
    <section className="overview-content">
      <section className="overview-grid" aria-label="概览指标">
      <MetricCard
        label="请求数量"
        value={countKnown(total?.requests)}
        icon={<Activity size={17} />}
      />
      <MetricCard
        label="渠道尝试"
        value={countKnown(total?.attempts)}
        help="包含重试。"
        icon={<Radio size={17} />}
      />
      <MetricCard
        label="Token 数量"
        value={tokenCount(total)}
        help={tokenLabel(total)}
        icon={<Layers3 size={17} />}
      />
      <MetricCard
        label="估算消费"
        value={money(total?.estimated_cost_usd)}
        help="按当前模型价格估算。"
        icon={<Wallet size={17} />}
      />
      <MetricCard
        label="渠道总并发"
        value={currentConcurrency == null ? "—" : count(currentConcurrency)}
        help={liveError ? "实时并发读取失败" : livePending ? "正在读取实时并发" : liveComplete ? "所列渠道的实时尝试，包含所有 API Key。" : `覆盖不完整 · 已知 ${knownLive.length} 项`}
        icon={<Gauge size={17} />}
      />
      <MetricCard
        label="缓存率"
        value={rate(cacheRate)}
        help="缓存读取 / 输入 token"
        icon={<Database size={17} />}
      />
      </section>
      <div className="overview-panels">
        <section className="overview-panel" aria-label="模型消费">
          <header className="overview-panel-header">
            <h3>模型消费</h3>
            <div className="overview-panel-actions">
              <button className="button small ghost" onClick={() => onLogs()}>请求日志</button>
              <button className="button small ghost" onClick={() => onLogs(undefined, true)}>失败请求</button>
            </div>
          </header>
          {rankedModels.length ? <table className="overview-table overview-models">
            <thead><tr><th scope="col">模型</th><th scope="col">Token</th><th scope="col">估算消费</th><th scope="col" title="占已估价消费的比例">占比</th></tr></thead>
            <tbody>{(showAllModels ? rankedModels : rankedModels.slice(0, previewCount)).map(item => {
              const top = offers.get(item.model);
              const cost = number(item.estimated_cost_usd);
              const share = knownModelCost > 0 && cost != null ? cost / knownModelCost : null;
              return <tr key={item.model}>
                <th scope="row"><button className="overview-link" title={top ? `${item.model} · 消费最高渠道：${channelName(top)}` : item.model} onClick={() => onLogs(item.model)}>{item.model}</button></th>
                <td title={tokenLabel(item)}>{tokenCount(item)}</td>
                <td>{money(cost)}</td>
                <td><span className="overview-share">{share == null ? "—" : rate(share)}
                  {share != null && <span className="overview-share-track" aria-hidden="true"><span style={{width:`${Math.min(share, 1) * 100}%`}} /></span>}
                </span></td>
              </tr>;
            })}</tbody>
          </table> : <p className="overview-empty">{historyError ? "模型消费暂不可用" : "暂无消费记录"}</p>}
          {rankedModels.length > previewCount && <button type="button" className="button small ghost overview-more"
            aria-expanded={showAllModels} onClick={() => setShowAllModels(value => !value)}>
            {showAllModels ? "收起" : `查看全部 ${rankedModels.length} 个模型`}
          </button>}
        </section>
        <section className="overview-panel" aria-label="当前渠道">
          <header className="overview-panel-header"><h3>当前渠道</h3></header>
          {priorityRows.length ? <table className="overview-table overview-channels">
            <thead><tr><th scope="col">渠道</th><th scope="col">状态</th><th scope="col">并发</th></tr></thead>
            <tbody>{priorityRows.slice(0, showAllChannels ? 12 : previewCount).map(row => (
              <tr key={rowId(row)}>
                <th scope="row"><button className="overview-link" title={[channelName(row), row.source_name, row.model].filter(Boolean).join(" · ")}
                  onClick={() => onChannel(row)}>{channelName(row)}</button></th>
                <td><Status row={row} /></td><td>{countKnown(live.get(rowId(row)))}</td>
              </tr>
            ))}</tbody>
          </table> : <p className="overview-empty">暂无渠道</p>}
          {priorityRows.length > previewCount && <button type="button" className="button small ghost overview-more"
            aria-expanded={showAllChannels} onClick={() => setShowAllChannels(value => !value)}>
            {showAllChannels ? "收起" : `查看更多 · ${Math.min(priorityRows.length, 12)}`}
          </button>}
        </section>
      </div>
    </section>
  );
}


const reveal = {
  initial: { opacity: 0, y: 12 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.32 },
};

function MetricCard({
  label,
  value,
  icon,
  help,
}: {
  label: string;
  help?: ReactNode;
  value: string;
  icon: ReactNode;
}) {
  return (
    <div className="metric-card">
      <div className="metric-card-label">
        {label}
        {help ? <Tip text={help}><span aria-label={`${label}说明`}>{icon}</span></Tip> : icon}
      </div>
      <div className="metric-card-number" title={value}>{value}</div>
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
  const {points, seconds} = useMemo(() => trafficBuckets(series.data?.data || [], series.data?.bucket_seconds || 60), [series.data]);
  const max = Math.max(1, ...points.map(p => p.success + p.failed));
  const [active, setActive] = useState(0), chartHint = useId();
  const current = Math.min(active,points.length - 1);
  const pointText = (p: typeof points[number]) =>
    p.samples ? `${dateTime(p.at)} · 成功 ${p.success} / 失败 ${p.failed} · 源区间峰值 ${p.peak}${p.covered ? "" : " · 覆盖不完整"}` : `${dateTime(p.at)} · 未采集`;
  return (
    <motion.section id="channel-trend" {...reveal} className="trend-panel">
      <div className="panel-heading">
        <div>
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
          <span>每 {seconds % 60 === 0 ? `${seconds / 60} 分钟` : `${seconds} 秒`}</span>
        </div>
      </div>
      {series.isPending ? (
        <DetailLoading kind="chart" label="请求趋势" />
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
            <div className="bar-chart" role="group" aria-label="渠道尝试趋势，方向键浏览各时间区间" aria-describedby={chartHint} tabIndex={0} onKeyDown={event => {
              if (!["ArrowLeft","ArrowRight","Home","End"].includes(event.key)) return;
              event.preventDefault();
              setActive(event.key === "Home" ? 0 : event.key === "End" ? points.length-1 :
                Math.min(points.length - 1, Math.max(0, current + (event.key === "ArrowLeft" ? -1 : 1))));
            }}>
              {points.map((p,i) => (
                <Tip
                  key={p.at}
                  keyboard={false}
                  text={pointText(p)}
                >
                  <span
                    className={`chart-column ${!p.covered ? "partial" : ""} ${i === current ? "current" : ""}`}
                    onPointerDown={() => setActive(i)}
                    style={{ height: "100%" }}
                  >
                    {!!p.samples && (p.success > 0 || p.failed > 0) && <span
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
                    </span>}
                  </span>
                </Tip>
              ))}
            </div>
          </div>
          <p id={chartHint} className="chart-inspection" aria-live="polite">{points[current] && pointText(points[current])}</p>
          <div className="chart-times">
            <span>{dateTime(points[0]?.at)}</span>
            <span>所选 key / 模型范围 · 重试分别计数</span>
            <span>{dateTime(points.at(-1)?.at)}</span>
          </div>
        </>
      )}
    </motion.section>
  );
}

function Detail({
  row,
  onClose,
  onLogs,
  balance,
  imports,
  catalog,
  site,
  check,
  trend,
  session,
}: {
  session: string;
  site?: string;
  check?: ChannelCheck;
  trend: Omit<CacheTrendProps, "row">;
  row: Channel | null;
  onClose: () => void;
  onLogs: (row: Channel) => void;
  balance?: SiteBalanceResult;
  imports?: SubImportsQuery;
  catalog: Channel[];
}) {
  if (!row) return null;
  const installed = imports?.data?.data.find((item) => item.source_id === row?.source_id && item.provider === row?.provider);
  const hasControls = !!row.source_id && trend.connection.account;
  return (
    <DetailDrawer key={rowId(row)} title={<SiteLink base={site}>{channelName(row)}</SiteLink>}
      context={[row.source_name || row.source_id, row.upstream_model && row.upstream_model !== row.model ? `${row.upstream_model} → ${row.model}` : row.model].filter(Boolean).join(" · ")}
      close={onClose} closeLabel="关闭详情"
      actions={<><Status row={row} /><button type="button" className="button small" onClick={() => onLogs(row)}>查看相关日志</button></>}
      pages={[
        { id: "overview", label: "概览", content: <>
              <div className="detail-section">
                <h3>时延分布</h3>
                <dl className="detail-stats">
                  <div><dt>请求前等待 p50</dt><dd>{ms(row.stats?.request_to_dispatch?.p50_ms)}</dd></div>
                  <div><dt>{latencyLabel(channelLatency(row.stats).kind)} p50</dt>
                    <dd><ResponseLatency created={row.stats?.response_created?.p50_ms} output={row.stats?.first_output?.p50_ms} text={row.stats?.first_text?.p50_ms} /></dd></div>
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
                    <dd>{dateTime(row.stats?.last_success_at)}</dd>
                  </div>
                  <div>
                    <dt>首字延迟 p95</dt>
                    <dd>{ms(channelLatency(row.stats).value?.p95_ms)}</dd>
                  </div>
                  <div>
                    <dt>最近一次首字延迟</dt>
                    <dd>{ms(channelLatency(row.stats).value?.last_ms)}</dd>
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
                    <Tip text={<FailureCounts channels={[row]} summary />}><span>成功率 <CircleHelp size={12} /></span></Tip>
                  </div>
                </div>
              </div>
              <div className="detail-section">
                <h3 title="同一站点账号下的渠道共享余额；负余额不会自动停用渠道。">站点余额</h3>
                <BalanceValue balance={balance?.data} loading={imports?.isPending} detail />
              </div>
              <ChannelDetailTrends key={`${providerId(row)}:${row.model}:trends`} row={row} {...trend} />
              <CacheTrend key={`${providerId(row)}:${row.model}:cache`} row={row} {...trend} />
        </> },
        ...(imports ? [{ id: "models", label: "模型", content: <ChannelModels key={`models:${providerId(row)}`} row={row} imports={imports} catalog={catalog} keyId={trend.keyId} session={session} /> }] : []),
        ...(hasControls ? [{ id: "checks", label: "检测", content: <>
              <ChannelDetailChecks key={`${providerId(row)}:${row.model}:checks`} row={row} />
              <section className="detail-section">
                <h3>降智检测</h3>
                {check ? <div className="quality-status-stack"><Tip text={check.history && check.history.total > 0 ? qualityTooltip(check.history, check.checked_at, check.origin) : `最近检测：${dateTime(check.checked_at)}`}><CheckVerdict result={check} /></Tip><QualityProbability history={check.history} checkedAt={check.checked_at} origin={check.origin} /></div> : <p className="muted">暂无检测结果。</p>}
                {row.source_id && <QualityHistory key={providerId(row)} path={`/v1/sources/${encodeURIComponent(row.source_id)}/channel-checks/history?${new URLSearchParams({ provider: row.provider })}`} revision={`${check?.checked_at}:${check?.history?.total}:${check?.history?.successful}`} title={check?.history_scope === "account_group" ? "共享检测记录" : "渠道检测记录"} />}
                {check?.history_scope !== "account_group" && boundGroups(installed).map(group => <QualityHistory key={`${group.account_id}:${group.group_id}`} path={`/v1/sub2api/accounts/${encodeURIComponent(group.account_id)}/groups/${group.group_id}/quality-history`} revision={`${check?.checked_at}:${check?.history?.total}:${check?.history?.successful}`} title={`sub2api 检测记录 · 分组 #${group.group_id}`} />)}
              </section>
        </> }, { id: "settings", label: "设置", content: <section className="detail-section" aria-label="渠道设置与访问">
                <div className="detail-channel-actions"><h3>设置与访问</h3><ChannelSettings row={row}/></div>
                <ChannelAccess key={`access:${providerId(row)}`} row={row} imports={imports} onRemoved={onClose} />
        </section> }] : []),
      ]} />
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
          <Dialog.Title>指标定义</Dialog.Title>
          <Dialog.Description className="sr-only">渠道指标的统计范围与口径</Dialog.Description>
          <div className="guide-items">
            {[
              [
                "请求前等待",
                "从收到请求到发起本次渠道尝试，含排队和前序重试。",
              ],
              [
                "首字延迟",
                "本次渠道请求开始到首个响应事件；详情区分响应创建与首个正文。各阶段的分位数不可相加。",
              ],
              [
                "余额不足",
                "已核验且所有关联站点账号的余额均 ≤ 0。未核验不等于正常。",
              ],
              [
                "费用与利润参考",
                "估算只用已确认价格；暂计账单只含已匹配记录。利润参考不作币种换算，不用于对账。",
              ],
              [
                "请求与尝试",
                "请求按最终结果计一次；一次请求可以有多次渠道尝试。历史指标受筛选影响，路由状态、并发和余额按渠道整体展示。",
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

export default function Dashboard({
  connection: baseConnection,
  disconnect,
  changeConnection,
}: {
  connection: Connection;
  disconnect: (reason?: string) => void;
  changeConnection: () => void;
}) {
  const [managementPosition, setManagementPosition] = useState<ManagementPagePosition>();
  const [priceDrafts, setPriceDrafts] = useState<PriceDrafts>(() => new Map());
  const [savingPrices, setSavingPrices] = useState(() => new Set<string>());
  useEffect(() => {
    if (!priceDrafts.size) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    globalThis.addEventListener("beforeunload", warn);
    return () => globalThis.removeEventListener("beforeunload", warn);
  }, [priceDrafts.size]);
  const preferenceScope = baseConnection.account
    ? JSON.stringify([baseConnection.base, baseConnection.session])
    : baseConnection.base;
  const [incoming] = useState(() => {
    const owner = history.state?.workspaceOwner;
    return readWorkspace(!owner || owner === preferenceScope ? location.hash : "", !!baseConnection.account);
  });
  const initialFilters = (scope: "channels" | "routing" | "balances" | "overview") => {
    const saved = loadFilters(preferenceScope, scope);
    return incoming.view === scope && incoming.filters
      ? {...incoming.filters, search: history.state?.workspaceOwner === preferenceScope ? saved.search : ""}
      : saved;
  };
  const [pageFilters, setPageFilters] = useState(() => ({
    channels: initialFilters("channels"),
    routing: initialFilters("routing"),
    balances: initialFilters("balances"),
    overview: initialFilters("overview"),
  }));
  const [view, setView] = useState<View>(() =>
    incoming.view || loadView(baseConnection.base, !!baseConnection.account),
  );
  const sortStorageScope = baseConnection.account ? JSON.stringify([baseConnection.base, baseConnection.session]) : baseConnection.base;
  const [sortPreferences, setSortPreferences] = useState(() => loadChannelSortPreferences(sortStorageScope, pageFilters.channels));
  const routeView = view === "routing";
  const channelView = view === "channels" || routeView;
  const channelScope = channelSortScope(pageFilters[routeView ? "routing" : "channels"]);
  const channelSort = scopedChannelSort(sortPreferences, channelScope);
  const routingSort = useRouteSorting(sortStorageScope);
  const [sortPreview, setSortPreview] = useState<{ scope: string; rows: Channel[] } | null>(null);
  const filterScope = view === "balances" || view === "overview" || routeView ? view : "channels";
  const needsChannelData = channelView || view === "balances" || view === "overview" || view === "automations";
  const filters = pageFilters[filterScope];
  const sourceQuery = useConsoleSources(baseConnection.session, !!baseConnection.account, view === "sources");
  useEffect(() => {
    if (baseConnection.account && view === "sources") void sourceQuery.refetch();
  }, [baseConnection.account, view, sourceQuery.refetch]);
  const sourceList = sourceQuery.data?.data || [];
  const noSources = !!baseConnection.account && sourceQuery.isSuccess && sourceList.length === 0;
  const imported = useSubImports(!!baseConnection.account && needsChannelData);
  const fingerprints = useModelFingerprints(!!baseConnection.account && channelView);
  const rawChecks = useChannelChecks(
    baseConnection.session,
    !!baseConnection.account && needsChannelData,
  );
  const subQuality = useSubQualitySummary(!!baseConnection.account && needsChannelData);
  const checks = withSubQuality(rawChecks, imported.data?.data || [], subQuality.data?.data || []);
  const { siteFor } = useChannelSites(baseConnection, imported.data?.data || [], needsChannelData);
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
    balanceTopN,
    balanceThreshold,
    statusFilter,
    failureReason,
    search,
    endpoint,
    stream,
  } = filters;
  const [page, setPage] = useState(0),
    [editingRoutes, setEditingRoutes] = useState<"channels" | "routing" | null>(null);
  const [filterTab, setFilterTab] = useState("conditions");
  const adjustingChannels = channelView && editingRoutes === view;
  const canEditRoutes = channelView && !!baseConnection.account;
  const previewingSort = channelView && !adjustingChannels && sortPreview?.scope === channelScope;
  const routeHelpId = useId();
  useEffect(() => setSortPreview(null), [channelScope, view]);
  useEffect(() => setFilterTab("conditions"), [view]);
  const sortRules = channelSort.rules;
  useEffect(() => {
    saveView(baseConnection.base, view);
  }, [baseConnection.base, view]);
  useEffect(() => {
    for (const scope of ["channels", "routing", "balances", "overview"] as const)
      saveFilters(preferenceScope, pageFilters[scope], scope);
  }, [preferenceScope, pageFilters]);
  useEffect(() => saveChannelSortPreferences(sortStorageScope, sortPreferences), [sortStorageScope, sortPreferences]);
  const activeFilters = (Object.keys(defaultFilters) as (keyof Filters)[]).filter(
    (key) =>
      ((channelView && ["keyId", "sourceId", "model", "window", "search", "balanceFilter", "statusFilter", "failureReason", "endpoint", "stream", "sort", "sortRules"].includes(key)) ||
        (view === "balances" && ["keyId", "sourceId", "model", "window", "search", "balanceTopN", "balanceThreshold"].includes(key))) &&
      !["sort", "sortRules", "window"].includes(key) && filters[key] !== defaultFilters[key],
  );
  const hasFilters = activeFilters.length > 0 || window !== defaultFilters.window;
  const [detailId, setDetailId] = useState<string | null>(incoming.detail),
    [showTrend, setShowTrend] = useState(false),
    [guide, setGuide] = useState(false),
    [menu, setMenu] = useState(false),
    [refresh, setRefresh] = useState(0);

  const [auto, setAuto] = useState(false);
  const [columnView, setColumnView] = useState<ChannelMetricLayout>("run");
  const [customColumns, setCustomColumns] = useTableColumns("uni-console:channel-columns:v1", observationColumns);
  const compactViewport = useCompactWorkbench();
  const compactObservation = compactViewport && !customColumns && columnView === "run";
  const columnChoices = baseConnection.account ? observationColumns : observationColumns.filter(({ id }) => id !== "quality" && id !== "fingerprint");
  const visibleColumns = customColumns ? customColumns.filter(id => columnChoices.some(choice => choice.id === id)) : observationPreset(columnView, compactObservation, !!baseConnection.account);
  const observationLayout = customColumns ? "custom" : columnView;
  const showBalanceColumn = visibleColumns.includes("balance");
  const showSpendColumn = visibleColumns.includes("actual") || visibleColumns.includes("profit");
  const qualityColumns = visibleColumns.includes("quality") || visibleColumns.includes("fingerprint");
  const { observeHeading, level: toolsLevel } = useWorkbenchTools();
  const inlineModel = !routeView && toolsLevel >= 1;
  const inlineKey = !routeView && toolsLevel >= 2;
  const inlineRoute = canEditRoutes && toolsLevel >= (routeView ? 1 : 3);
  const [balanceView, setBalanceView] = useState("channels");
  const [balanceDetails, setBalanceDetails] = useState(false);
  const [locatedChannel, setLocatedChannel] = useState<Channel | null>(null);
  const [locator, setLocator] = useState(false);
  const locatorOpener = useRef<HTMLElement | null>(null);
  const detailOpener = useRef<HTMLElement | null>(null);
  const emptyChannels = useMemo<Channel[]>(() => [], []);
  const locatorCatalog = useQuery({
    queryKey: ["locator-catalog", baseConnection.session, baseConnection.base],
    queryFn: ({ signal }) => request<Catalog>(baseConnection, "/v1/model-channels?" + channelParams("", "15m", "", "all", "all"), signal),
    enabled: locator && !noSources,
  });
  function openLocator() {
    locatorOpener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setLocator(true);
  }
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (event.isComposing || event.altKey || !(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k" || document.querySelector('[role="dialog"]')) return;
      event.preventDefault();
      openLocator();
    };
    globalThis.addEventListener("keydown", shortcut);
    return () => globalThis.removeEventListener("keydown", shortcut);
  }, []);
  const [walletDialog,setWalletDialog] = useState<string|null>(null);
  const [theme, setTheme] = useTheme();
  const deferredSearch = useDeferredValue(search);
  const keys = useQuery({
    enabled: needsChannelData || (!baseConnection.account && view === "prices"),
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
  const effectiveWindow = window;
  const effectiveEndpoint = view === "balances" ? "all" : endpoint;
  const effectiveStream = view === "balances" ? "all" : stream;
  const keyRequestStats = useKeyRequestStats(connection, {
    range: effectiveWindow, model, endpoint: effectiveEndpoint, stream: effectiveStream,
  }, (channelView || view === "balances") && (baseConnection.account || keysLoaded), auto);
  const params = channelParams(
    keyId,
    effectiveWindow,
    "",
    effectiveEndpoint,
    effectiveStream,
  );
  const catalog = useQuery({
    queryKey: ["catalog", connection.session, keyId, effectiveEndpoint, effectiveStream],
    queryFn: ({ signal }) =>
      request<Catalog>(connection, "/v1/model-channels?" + params, signal),
    enabled:
      needsChannelData &&
      (baseConnection.account || keysLoaded) &&
      !keyRemoved &&
      (!baseConnection.account || !sourceQuery.isSuccess || sourceList.length > 0),
  });
  const prices = useQuery({
    queryKey: ["prices", connection.session],
    queryFn: ({ signal }) =>
      analyticsRequest<{ data: ModelPrice[] }>(
        connection,
        "/analytics/v1/prices",
        signal,
      ),
    enabled: (baseConnection.account || keysLoaded) && view === "prices",
    refetchInterval: initializationRetryInterval,
    staleTime: 60_000,
  });
  // The catalog supplies model choices independent of the selected model.
  const statisticsModel = channelView || view === "overview" ? model : "";
  const metricsParams = channelParams(keyId, effectiveWindow, statisticsModel, effectiveEndpoint, effectiveStream);
  const metricsKey = ["metrics", connection.session, keyId, effectiveWindow, effectiveEndpoint, effectiveStream, "statistics-v3", statisticsModel];
  const metrics = useQuery({
    queryKey: metricsKey,
    queryFn: ({ signal }) =>
      readMetrics(
        connection,
        "/v1/channel-metrics?" + metricsParams,
        signal,
        effectiveEndpoint,
        effectiveStream,
      ),
    staleTime: 30_000,
    enabled: needsChannelData && !noSources && !keyRemoved && (baseConnection.account || (keysLoaded && !!catalog.data)),
    refetchInterval: (query) => initializationRetryInterval(query) || (auto ? 30_000 : false),
    refetchIntervalInBackground: false,
  });
  const liveMetrics = useQuery({
    queryKey: ["live-metrics", connection.session, keyId, effectiveEndpoint, effectiveStream],
    queryFn: ({ signal }) =>
      request<Metrics>(
        connection,
        "/v1/channel-metrics?" +
          channelParams(keyId, "1m", "", effectiveEndpoint, effectiveStream),
        signal,
      ),
    enabled:
      !keyRemoved &&
      !noSources &&
      (baseConnection.account || (keysLoaded && !!catalog.data)) &&
      (view === "overview" || channelView),
    staleTime: 2_000,
    refetchInterval: auto ? 5_000 : false,
    refetchIntervalInBackground: false,
  });
  const balanceModelMetrics = useBalanceModelMetrics(connection, {
    range: effectiveWindow, model, keyId, to: metrics.data?.to, refresh, auto,
    enabled: view === "balances" && balanceView === "channels" && balanceDetails && keysLoaded && !keyRemoved && !!metrics.data,
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
        (liveMetrics.data?.data || []).filter(row => view !== "overview" || !model || row.model === model).map((row) => [
          rowId(row),
          row.stats?.inflight,
        ]),
      ),
    [liveMetrics.data, view, model],
  );
  const queryClient = useQueryClient();
  const cachedDashboardAt = restoredDashboardAt(queryClient, metricsKey);
  const snapshotScope = JSON.stringify(metricsKey);
  useEffect(() => {
    if (!baseConnection.account || !channelView) return;
    const scope = JSON.parse(snapshotScope);
    let timer: ReturnType<typeof setTimeout>;
    let idle: number | undefined;
    const save = () => {
      clearTimeout(timer);
      if (idle != null) globalThis.cancelIdleCallback?.(idle);
      timer = setTimeout(() => {
        const write = () => saveDashboardSnapshot(queryClient, baseConnection.session, scope);
        if (typeof globalThis.requestIdleCallback === "function") idle = globalThis.requestIdleCallback(write, {timeout:600});
        else write();
      }, 150);
    };
    const unsubscribe = queryClient.getQueryCache().subscribe(event => {
      if (event.type === "updated" && event.action.type === "success" &&
          event.query.isActive() && dashboardSnapshotKey(event.query.queryKey)) save();
    });
    save();
    return () => { clearTimeout(timer); if (idle!=null) globalThis.cancelIdleCallback?.(idle); unsubscribe(); };
  }, [queryClient, baseConnection.account, baseConnection.session, channelView, snapshotScope]);
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
      (keyRemoved || modelRemoved || sourceRemoved || (error && !catalog.data))
        ? []
        : catalogMetrics(catalog.data, metrics.data)
            .filter((row) => !model || row.model === model)
            .map((row) => channelBilling(row, imported.data)),
    [catalog.data, metrics.data, model, error, imported.data, keyRemoved, modelRemoved, sourceRemoved],
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
    enabled: !!baseConnection.account && (routeView || adjustingChannels),
  });
  const tableRows = adjustingChannels ? controls.arrange(rows)
    : previewingSort ? previewChannelOrder(rows, sortPreview.rows)
    : routeView ? controls.arrange(rows, true) : rows;
  const rowRanks = useMemo(
    () => new Map(tableRows.map((row, i) => [rowId(row), i + 1])),
    [tableRows],
  );
  const text = deferredSearch.toLowerCase();
  const knownFailures = useMemo(() => {
    const kinds = new Set(["compaction_invalid_output", "compaction_stream_failed", "compaction_unsupported"]);
    if (!channelView) return kinds;
    for (const row of tableRows) {
      let classified = 0;
      for (const [reason, qty] of Object.entries(row.stats?.failure_reasons || {})) {
        if (!Number.isFinite(qty) || qty <= 0) continue;
        classified += qty;
        if (/^[a-z][a-z0-9_]{0,79}$/.test(reason)) kinds.add(reason);
      }
      if ((row.stats?.failed || 0) > classified) kinds.add("unknown");
    }
    if (failureReason && failureReason !== "compaction") kinds.add(failureReason);
    return kinds;
  }, [channelView, tableRows, failureReason]);
  const candidateRows = tableRows.filter(row =>
    (!text || `${channelName(row)} ${row.provider} ${row.model} ${row.upstream_model}`.toLowerCase().includes(text)) &&
    (!channelView || !statusFilter || (statusFilter === "eligible" ? row.eligible : !row.eligible)) &&
    (!channelView || !failureReason || (() => {
      const failures = row.stats?.failure_reasons || {};
      if (failureReason === "unknown")
        return (failures.unknown || 0) > 0 || (row.stats?.failed || 0) > Object.values(failures).reduce((n, qty) => n + (Number.isFinite(qty) && qty > 0 ? qty : 0), 0);
      if (failureReason === "compaction") return ["compaction_invalid_output", "compaction_stream_failed", "compaction_unsupported"].some(reason => (failures[reason] || 0) > 0);
      return (failures[failureReason] || 0) > 0;
    })()));
  const balanceRanks = view === "balances" ? rankBalanceProviders(candidateRows, balanceTopN) : null;
  const balanceThresholdValid = !balanceThreshold || (Number.isFinite(Number(balanceThreshold)) && Number(balanceThreshold) >= 0);
  const balanceCatalog = balanceRanks?.providers || [...new Set(candidateRows.map(providerId))];
  const pageBounds = (n: number) => Math.min(page,Math.max(0,Math.ceil(n/25)-1))*25;
  const actualRange = useMemo(() => actualCostRange(window), [window]);
  const auditScope = JSON.stringify([view, preferenceScope, selectedSourceId, keyId, model, window, endpoint, stream, search, statusFilter, failureReason, balanceFilter, balanceTopN, balanceThreshold, actualRange.startDate, actualRange.endDate]);
  const [balanceAudit, setBalanceAudit] = useState<{ scope: string; targets: string[] } | null>(null);
  const activeAudit = balanceAudit?.scope === auditScope ? balanceAudit : null;
  useEffect(() => setBalanceAudit(previous => previous?.scope === auditScope ? previous : null), [auditScope]);
  const balanceScope = useMemo(() => new Set(balanceCatalog), [balanceCatalog]);
  const visibleBalanceProviderIds = new Set(
    view==="balances" ? balanceCatalog.slice(pageBounds(balanceCatalog.length),pageBounds(balanceCatalog.length)+25)
      : channelView && showBalanceColumn ? candidateRows.slice(pageBounds(candidateRows.length),pageBounds(candidateRows.length)+25).map(providerId) : [],
  );
  const focusedBalance = detailId ? rows.find(row=>rowId(row)===detailId) : locatedChannel;
  if (focusedBalance) visibleBalanceProviderIds.add(providerId(focusedBalance));
  // An explicit balance criterion needs coverage beyond the visible page.
  const needsBalanceCoverage = channelView ? !!balanceFilter : view === "balances" && !!balanceThreshold && balanceThresholdValid;
  const balanceTargets = [...new Set([...(activeAudit?.targets || []), ...(needsBalanceCoverage ? balanceCatalog : []), ...visibleBalanceProviderIds])];
  const balanceEnabled = channelView || view === "balances" || !!focusedBalance;
  const pollBalances = auto && !activeAudit && !needsBalanceCoverage;
  const balanceMap = useChannelAccountBalances(balanceEnabled ? balanceTargets : [], imported.data?.data || [], baseConnection.session, !!baseConnection.account && balanceEnabled, pollBalances);
  const balanceLoading = !!baseConnection.account && imported.isPending;
  // Standalone gateways still expose historical deductions via this legacy
  // route. Only retain the cost field; never use this result as a wallet.
  const historicalCostTargets = !baseConnection.account && actualRange.supported
    ? channelView && visibleColumns.includes("actual")
      ? [...new Set(candidateRows.slice(pageBounds(candidateRows.length), pageBounds(candidateRows.length) + 25).map(providerId))]
      : view === "balances" && balanceView === "channels" && balanceDetails && !balanceThreshold
        ? [...visibleBalanceProviderIds] : []
    : [];
  const historicalCostQueries = useQueries({
    queries: historicalCostTargets.map((provider) => ({
      queryKey: [
        "historical-channel-cost",
        connection.session,
        provider,
        model,
        actualRange.startDate,
        actualRange.endDate,
      ],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        scheduleBalance(
          balanceSite(
            siteFor({ source_id: JSON.parse(provider)[0], provider: JSON.parse(provider)[1] }),
            JSON.parse(provider)[0] || connection.base,
          ),
          async () =>
            (await request<{ actual_cost_usd?: number | null }>(
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
            )).actual_cost_usd ?? null,
          signal,
          JSON.parse(provider)[0] || connection.sourceId || connection.base,
        ),
      staleTime: 300_000,
      refetchInterval: auto ? 300_000 : false,
      refetchIntervalInBackground: false,
      retry: false,
    })),
  });
  const historicalCostMap = new Map(historicalCostTargets.map((provider, index) => [provider, historicalCostQueries[index]]));
  const balanceQueries = [...balanceMap.values()];
  const groupedWallets = walletGroups(view === "balances" ? balanceMap : []);
  const pendingBalances = balanceQueries.filter(
    (query) => query.isPending,
  ).length;
  let lowCount = 0, knownBalances = 0;
  for (const [provider, query] of balanceMap) {
    if (!balanceScope.has(provider) || !balanceIsKnown(query.data)) continue;
    knownBalances++;
    if (balanceIsLow(query.data)) lowCount++;
  }
  const unknownBalances = Math.max(0,balanceCatalog.length - knownBalances);
  const channelDataComplete = !!catalog.data && !!metrics.data && !!liveMetrics.data && !!keyRequestStats.data &&
    (!baseConnection.account || (!!queryClient.getQueryData(["channel-checks", baseConnection.session]) && !!subQuality.data && !!imported.data && !!sourceQuery.data));
  const filtered = channelView && balanceFilter ? candidateRows.filter(row => {
    const result = balanceMap.get(providerId(row));
    return balanceIsLow(result?.data);
  }) : candidateRows;
  const firstProviders = useMemo(() => {
    const index = (items: Channel[]) => {
      const result = new Map<string, Channel>();
      for (const item of items) {
        const id = providerId(item);
        if (!result.has(id)) result.set(id, item);
      }
      return result;
    };
    return view === "balances" ? {filtered: index(filtered), all: index(rows)} : null;
  }, [view, filtered, rows]);
  let thresholdUnknown = 0;
  const balanceProviders = (balanceRanks?.providers || []).filter(provider => {
    if (!balanceThreshold || !balanceThresholdValid) return true;
    const query = balanceMap.get(provider);
    const state = balanceThresholdState(query?.data, balanceThreshold);
    if (state === "unknown") thresholdUnknown++;
    return state === "below";
  });
  const channelSpend = useScopedChannelSpend({ rows: filtered, session: connection.session, sourceId: selectedSourceId, keyId, model, endpoint: effectiveEndpoint, stream: effectiveStream, from: metrics.data?.from, to: metrics.data?.to, snapshot: metrics.data, snapshotError: metrics.isError, snapshotUpdatedAt: metrics.dataUpdatedAt, refresh, auto, paused: metrics.isFetching, enabled: (view === "balances" && balanceView === "channels" && balanceDetails || channelView && showSpendColumn) && !!baseConnection.account });
  const balanceFinance = useMemo(() => {
    const result = new Map<
      string,
      {
        estimated: number | null;
        actual: number | null;
        profit: number | null;
        actualUpperBound: boolean;
        provisional: boolean;
        missingPrices: number;
        actualLabel: string;
        actualExplanation: string;
      }
    >();
    if (!balanceRanks || !balanceDetails) return result;
    const byProvider = new Map<string, Channel[]>();
    for (const row of filtered) {
      const id = providerId(row);
      const group = byProvider.get(id);
      if (group) group.push(row);
      else byProvider.set(id, [row]);
    }
    for (const provider of balanceRanks.providers) {
      const providerRows = byProvider.get(provider)!;
      const missingPrices = providerRows.filter(row => {
        const stats = row.stats;
        return ((stats?.usage_samples ?? 0) > 0 || (stats?.input_tokens ?? 0) > 0 || (stats?.output_tokens ?? 0) > 0) &&
          !(typeof stats?.estimated_cost_usd === "number" && Number.isFinite(stats.estimated_cost_usd));
      }).length;
      const estimatedValues = providerRows
        .map((row) => row.stats?.estimated_cost_usd)
        .filter((value): value is number => typeof value === "number" && Number.isFinite(value));
      const estimated = estimatedValues.length
        ? estimatedValues.reduce((sum, value) => sum + value, 0)
        : null;
      // Analytics already aggregates provider deductions over the selected
      // window, including rolling ranges such as 1h. Prefer those values over
      // the calendar-day balance endpoint so the actual and estimated columns
      // share the same time range.
      const analyticsCosts = providerRows.map((row) => {
        const stats = row.stats;
        const actual = stats?.actual_cost_usd;
        const samples = stats?.actual_cost_samples;
        const hasUsage =
          (typeof stats?.usage_samples === "number" && stats.usage_samples > 0) ||
          (typeof stats?.estimated_cost_usd === "number" && Number.isFinite(stats.estimated_cost_usd));
        if (!hasUsage && !(typeof samples === "number" && samples > 0)) return null;
        const hasSamples =
          typeof samples === "number" ? samples > 0 : hasUsage && typeof actual === "number";
        return hasSamples && typeof actual === "number" && Number.isFinite(actual)
          ? actual
          : undefined;
      });
      const observedAnalyticsCosts = analyticsCosts.filter(
        (value): value is number | undefined => value !== null,
      );
      const analyticsActual =
        observedAnalyticsCosts.length > 0 && observedAnalyticsCosts.every((value): value is number => value !== undefined)
          ? observedAnalyticsCosts.reduce((sum, value) => sum + value, 0)
          : undefined;
      const spend = balanceSpend(providerRows.map(row => channelSpend.get(rowId(row))));
      const historicalCost = historicalCostMap.get(provider)?.data;
      const fallbackActual = typeof historicalCost === "number" && Number.isFinite(historicalCost) ? historicalCost : null;
      const actual = baseConnection.account ? spend.actual : analyticsActual ?? fallbackActual;
      const actualUpperBound = !!baseConnection.account && spend.upperBound;
      const revenue = estimated == null
        ? null
        : providerRows.reduce((sum, row) => {
            const value = row.stats?.estimated_cost_usd;
            return typeof value === "number" && Number.isFinite(value)
              ? sum + value * (salePercent({
                  model: row.model,
                  sale_percent: row.stats?.sale_percent,
                }) / 100) * 6.9
              : sum;
          }, 0);
      result.set(provider, {
        estimated,
        actual,
        profit: !missingPrices && revenue != null && actual != null ? revenue - actual : null,
        actualUpperBound,
        provisional: !!baseConnection.account && spend.provisional,
        missingPrices,
        actualLabel: baseConnection.account ? spend.label : "—",
        actualExplanation: baseConnection.account ? spend.explanation : "",
      });
    }
    return result;
  }, [historicalCostMap, balanceRanks, baseConnection.account, channelSpend, filtered, balanceDetails]);
  const observedDetection = useObservedDetection(baseConnection.session, channelView && !!baseConnection.account, filtered, checks, fingerprints);
  const checkingChannels = observedDetection.expanded;
  const total = channelView ? filtered.length : balanceProviders.length;
  const pageCount = Math.max(1, Math.ceil(total / 25)),
    currentPage = Math.min(page, pageCount - 1);
  const pageRows = filtered.slice(currentPage * 25, (currentPage + 1) * 25),
    pageProviders = balanceProviders.slice(
      currentPage * 25,
      (currentPage + 1) * 25,
    );
  const detail = focusedBalance || null;
  const busy = metrics.isFetching || catalog.isFetching || keys.isFetching;
  const routePage = new Map(adjustingChannels ? pageRows.map(row => [rowId(row), row]) : []);
  const routeDrag = useChannelRouteDrag(JSON.stringify([adjustingChannels, auditScope, currentPage, controls.pending]), routePage,
    (from, to, after) => controls.moveRelative(from, to, after, pageRows));
  function setFilter(name: keyof Filters, value: string) {
    setPage(0);
    setPageFilters((current) => ({
      ...current,
      [filterScope]: { ...current[filterScope], [name]: value },
    }));
  }
  function reload() {
    if (
      baseConnection.account &&
      (view === "sources" || (noSources && view !== "prices"))
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
    if (view === "overview") {
      if (baseConnection.account) void sourceQuery.refetch();
      void keys.refetch();
      if (!keyRemoved) {
        void catalog.refetch();
        void metrics.refetch();
        void liveMetrics.refetch();
      }
      return;
    }
    if (channelView && baseConnection.account && !controls.pending) {
      void queryClient.invalidateQueries({ queryKey: ["channel-controls"] });
      void queryClient.invalidateQueries({ queryKey: ["control-catalog"] });
    }
    if (baseConnection.account && channelView) { void checks.refetch(); void subQuality.refetch(); }
    void keys.refetch();
    void keyRequestStats.refetch();
    if (channelView) void liveMetrics.refetch();
    if (
      keysLoaded &&
      !keyRemoved &&
      (!connection.account || sourceList.length > 0)
    ) {
      void catalog.refetch();
      void metrics.refetch();
    }
    setRefresh((x) => x + 1);
  }
  function refreshBalances() {
    void queryClient.refetchQueries({type:"active", predicate:query=>{
      const key=query.queryKey;
      return key[0]==="site-account-balance" && key[1]===baseConnection.session;
    }},{cancelRefetch:false});
  }
  const refreshBusy =
    baseConnection.account && (view === "sources" || (noSources && view !== "prices"))
      ? sourceQuery.isFetching
      : view === "prices"
        ? prices.isFetching
        : busy || (view === "overview" && (liveMetrics.isFetching || sourceQuery.isFetching));
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
    if (view !== next) {
      history.pushState({workspaceOwner:preferenceScope}, "", workspaceHash(next, hasChannelScope(next) ? pageFilters[next] : undefined));
      setDetailId(null);
      setLocatedChannel(null);
      setBalanceAudit(null);
      setShowTrend(false);
      focusWorkspace.current = true;
    }
    setView(next);
    setPage(0);
    setMenu(false);
  }
  function openRelatedLogs(nextModel?: string, failed = false, target?: Channel) {
    const sourceId = target?.source_id || selectedSourceId;
    const carryKey = sourceId === selectedSourceId || !selectedSourceId && (!keyId.includes("::") || keyId.split("::")[0] === sourceId);
    const scope = {...defaultFilters, model:nextModel || model, sourceId, keyId:carryKey ? keyId : "", window, endpoint, stream,
      statusFilter:failed ? "failed" : ""};
    const from = (metrics.data?.from || 0) * 1000, to = (metrics.data?.to || 0) * 1000;
    history.pushState(null, "", requestLocation(scope,null,validLogWindow(from,to) ? {from,to} : null));
    setView("requests");
    setDetailId(null);
    setLocatedChannel(null);
    setMenu(false);
    focusWorkspace.current = true;
  }
  function openDetail(id: string) {
    detailOpener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setLocatedChannel(null);
    history.pushState({channelDetail:true, workspaceOwner:preferenceScope}, "", workspaceHash(view, filters, id));
    setDetailId(id);
  }
  function closeDetail() {
    if (locatedChannel) setLocatedChannel(null);
    else if (history.state?.channelDetail) history.back();
    else {
      history.replaceState({workspaceOwner:preferenceScope}, "", workspaceHash(view, filters));
      setDetailId(null);
    }
    requestAnimationFrame(() => detailOpener.current?.focus({preventScroll:true}));
  }
  const mainRef = useRef<HTMLElement>(null);
  const focusWorkspace = useRef(false);
  useEffect(() => {
    if (focusWorkspace.current) {
      focusWorkspace.current = false;
      if (globalThis.scrollY > 0) globalThis.scrollTo({top:0,behavior:"instant"});
      const title = mainRef.current?.querySelector<HTMLElement>("h2");
      if (title) {
        title.tabIndex = -1;
        title.focus({preventScroll:true});
      } else mainRef.current?.focus({preventScroll:true});
    }
  }, [view]);
  useEffect(() => {
    const owner = history.state?.workspaceOwner;
    if ((owner && owner !== preferenceScope) || !readWorkspace(location.hash, !!baseConnection.account).view)
      history.replaceState({workspaceOwner:preferenceScope}, "", workspaceHash(view));
    const restore = () => {
      if (history.state?.workspaceOwner && history.state.workspaceOwner !== preferenceScope) return;
      const next = readWorkspace(location.hash, !!baseConnection.account);
      if (!next.view) return;
      const scope = next.view;
      setView(scope);
      setDetailId(next.detail);
      setLocatedChannel(null);
      if (scope !== "channels" && scope !== "routing") setEditingRoutes(null);
      setBalanceAudit(null);
      if (next.filters && hasChannelScope(scope)) {
        const sharedFilters = next.filters;
        setPageFilters(current => ({...current, [scope]: {...sharedFilters, search:current[scope].search}}));
      }
      setPage(0);
      setMenu(false);
    };
    globalThis.addEventListener("popstate", restore);
    return () => globalThis.removeEventListener("popstate", restore);
  }, [baseConnection.account, preferenceScope]);
  useEffect(() => {
    if (!hasChannelScope(view)) return;
    history.replaceState({...history.state, workspaceOwner:preferenceScope}, "", workspaceHash(view, filters, detailId));
  }, [view, filters, detailId, preferenceScope]);
  const nav = (
    <ConsoleNavigation
      view={view}
      account={!!baseConnection.account}
      onSelect={selectView}
    />
  );
  const historyImport = metrics.data?.import;
  const historyMessages = [
    historyInitializing && (metrics.data ? "历史数据正在初始化，暂显示上次成功读取的数据。" : metrics.error?.message),
    !historyInitializing && staleSources.size > 0 && `历史同步滞后：${staleSourceNames.join("、") || "当前来源"}。统计可能缺少近期请求。`,
    historyImport && !historyImport.caught_up && (!historyImport.scanning || historyImport.audit_complete === false || !!historyImport.error_class || historyImport.remaining_objects > 0) &&
      (historyImport.error_class ? `采集异常：${historyImport.error_class}` : historyImport.audit_complete === false ? "来源上报尚未追平或存在记录缺口。" : "正在接收请求记录，统计尚不完整。"),
    metrics.data?.coverage === "partial" && "当前筛选范围的历史记录不完整。",
  ];
  const observationMessages = [
    ...historyMessages,
    previewingSort && "排序预览 · 未应用到路由",
    !!error && rows.length > 0 && `刷新失败，保留当前数据。${error}`,
    view === "balances" && !balanceThresholdValid && "余额阈值需为非负金额；已保留上次条件。",
    channelView && !!cachedDashboardAt && (metrics.isFetching ? "上次完整数据 · 正在后台更新" : "当前显示上次完整数据"),
    channelView && (qualityColumns || checkingChannels) && checks.error && `最近检测结果读取失败：${checks.error.message}`,
    channelView && balanceFilter && unknownBalances > 0 && `仅显示已核验且不足的渠道 · ${unknownBalances} 个未核验`,
    view === "balances" && thresholdUnknown > 0 && `${thresholdUnknown} 个渠道未能判定阈值；仅显示已确认结果。`,
    channelView && checkingChannels && (observedDetection.notice?.text || observedDetection.batchIssue),
  ];
  const routeButton = <button className="button small route-edit-trigger" type="button"
    aria-pressed={adjustingChannels} disabled={controls.pending || routingSort.busy}
    onClick={event => {
      if (!canEditRoutes || (view !== "channels" && view !== "routing")) return;
      const disclosure = event.currentTarget.closest<HTMLDetailsElement>(".workbench-disclosure");
      if (disclosure) disclosure.open = false;
      setEditingRoutes(view);
      requestAnimationFrame(() => mainRef.current?.querySelector<HTMLElement>(".route-editor-actions button:not(:disabled)")?.focus({preventScroll: true}));
    }}><SlidersHorizontal size={14} aria-hidden="true" />调整顺序</button>;
  const finishRouteEdit = () => {
    controls.discardAll();
    setEditingRoutes(null);
    requestAnimationFrame(() => mainRef.current?.querySelector<HTMLElement>(
      inlineRoute ? ".workbench-tools .route-edit-trigger" : ".channel-filter-disclosure > summary",
    )?.focus({preventScroll:true}));
  };
  const hasRouteDrafts = controls.drafts.size > 0;
  const routeActions = <div className="route-editor-actions" role="group" aria-label="请求顺序编辑">
    <button type="button" className={`button small ${hasRouteDrafts ? "primary" : ""}`}
      aria-label={hasRouteDrafts ? "应用全部临时修改" : "完成调整"}
      title={hasRouteDrafts ? "应用所有范围的顺序和停用修改" : "退出顺序编辑"}
      disabled={controls.pending} aria-busy={controls.pending || undefined}
      onClick={() => hasRouteDrafts ? void controls.applyAll() : finishRouteEdit()}>
      {controls.pending ? <Spinner small /> : <Check size={13} aria-hidden="true" />}{hasRouteDrafts ? "应用" : "完成"}
    </button>
    <button type="button" className="button small" aria-label="取消调整" title="退出并放弃未应用的修改"
      disabled={controls.pending} onClick={finishRouteEdit}><X size={14} aria-hidden="true" /></button>
  </div>;
  const timeFilter = <label className="select-field time-select">
    <Clock3 size={15} aria-hidden="true" />
    <select aria-label="时间范围筛选" value={window} onChange={event => setFilter("window", event.target.value)}>
      {ranges.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
    </select>
    <ChevronDown size={13} aria-hidden="true" />
  </label>;
  const routeIssues = adjustingChannels && !controls.pending && controls.draftIssues.length ?
    `未应用的修改已保留：${controls.draftIssues.map(issue =>
      `${sourceList.find(source => source.id === issue.source)?.name || issue.source}：${issue.message}`).join("；")}` : "";
  const apiKeyFilter = <div className="select-field key-select">
    <KeyRound size={15} aria-hidden="true" />
    <select aria-label="API key 筛选" value={keyId} onChange={e => setFilter("keyId", e.target.value)}>
      <option value="">全部 API Key</option>
      {keyRemoved && <option value={keyId}>已移除的 API key</option>}
      {keys.data?.data.map(key => <option key={key.key_id} value={key.key_id}>
        {key.source_name ? `${key.source_name} · ` : ""}Key {key.position} · {key.prefix}
      </option>)}
    </select>
    <ChevronDown size={13} aria-hidden="true" />
  </div>;
  const modelFilter = <div className="select-field model-select">
    <Layers3 size={15} aria-hidden="true" />
    <select aria-label="模型筛选" value={model} onChange={e => setFilter("model", e.target.value)}>
      <option value="">全部模型</option>
      {modelRemoved && <option value={model}>{model} · 未配置</option>}
      {models.map(name => <option key={name}>{name}</option>)}
    </select>
    <ChevronDown size={13} aria-hidden="true" />
  </div>;
  const observationHeader = <>
    {routeView && <th className="rank">#</th>}
    <th className="channel-identity">渠道</th>
    {checkingChannels && <th>检测操作</th>}
    <ChannelMetricHeaders keySelected={!!keyId} channels={filtered} columns={visibleColumns} renderDetection={id => id === "quality"
      ? <th className="column-quality"><Tip text="显示此来源、此渠道最近一次 gpt-6-astra 检测结果，与当前指标时间范围无关；不代表表中每个模型都已单独检测。">是否降智 <CircleHelp size={12} /></Tip></th>
      : <th className="column-quality"><Tip text={`显示此来源、此渠道、当前模型的最新指纹结果，与渠道管理和详情共享数据，与指标时间范围无关。${fingerprintNote}`}>指纹 <CircleHelp size={12}/></Tip></th> } />
  </>;
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
          onLocate={openLocator}
          onGuide={() => setGuide(true)}
          identity={baseConnection.account ? baseConnection.session : undefined}
          account={!!baseConnection.account}
          onDisconnect={() => disconnect()}
          onConnection={() =>
            baseConnection.account ? selectView("sources") : changeConnection()
          }
        />
        <main className="workspace" aria-label="工作区" tabIndex={-1} ref={mainRef}><PageBoundary key={view}><Suspense fallback={<PageLoading view={view} />}>
          {view === "overview" && !noSources && <header className="data-heading overview-heading" aria-label="总览统计范围">
            <div className="data-title"><h2>总览</h2><WorkbenchNotice messages={[...historyMessages, error && `读取不完整：${error}`]} /></div>
            <div className="overview-tools">
              <label className="overview-select"><Clock3 size={14} aria-hidden="true" /><select aria-label="总览时间范围" value={window} onChange={event => setFilter("window",event.target.value)}>
                {ranges.map(([value,label]) => <option value={value} key={value}>{label}</option>)}
              </select><ChevronDown size={12} aria-hidden="true" /></label>
              {baseConnection.account && <label className="overview-select"><Server size={14} aria-hidden="true" /><select aria-label="总览来源" value={selectedSourceId} onChange={event => {
                setFilter("sourceId",event.target.value); setFilter("keyId","");
              }}>
                <option value="">全部来源</option>
                {sourceRemoved && <option value={selectedSourceId}>来源已移除</option>}
                {sourceList.map(source => <option value={source.id} key={source.id}>{source.name}</option>)}
              </select><ChevronDown size={12} aria-hidden="true" /></label>}
              <label className="overview-select"><KeyRound size={14} aria-hidden="true" /><select aria-label="总览调用 Key" value={keyId} onChange={event => setFilter("keyId",event.target.value)}>
                <option value="">全部 Key</option>
                {keyRemoved && <option value={keyId}>Key 已移除</option>}
                {keys.data?.data.map(key => <option value={key.key_id} key={key.key_id}>{key.source_name ? key.source_name + " · " : ""}Key {key.position} · {key.prefix}</option>)}
              </select><ChevronDown size={12} aria-hidden="true" /></label>
              <label className="overview-select"><Layers3 size={14} aria-hidden="true" /><select aria-label="总览模型" value={model} onChange={event => setFilter("model",event.target.value)}>
                <option value="">全部模型</option>
                {model && !models.includes(model) && <option value={model}>{model}（当前目录未确认）</option>}
                {models.map(value => <option value={value} key={value}>{value}</option>)}
              </select><ChevronDown size={12} aria-hidden="true" /></label>
              <button type="button" className="button small overview-refresh" disabled={refreshBusy} aria-label="刷新数据"
                title="刷新数据" onClick={reload}><RefreshCw size={15} className={refreshBusy ? "spin" : ""} /></button>
            </div>
          </header>}
          {view === "requests" ? <RequestLogsPage connection={baseConnection} sources={sourceList}/> : view === "sub2api" && baseConnection.account ? (
            <Sub2apiChecks
              key={baseConnection.session}
              user={baseConnection.session}
              initialPosition={managementPosition}
              onPositionChange={setManagementPosition}
              onRouting={() => selectView("routing")}
            />
          ) : baseConnection.account && view === "sources" ? (
            <SourceSettings
              sources={sourceList}
              loading={sourceQuery.isPending}
              readError={sourceQuery.error?.message}
              refreshAction={refreshButton}
              onSaved={() => {
                queryClient.removeQueries({ queryKey: ["channel-import-keys"] });
                queryClient.removeQueries({ queryKey: ["sub-import-options"] });
                queryClient.removeQueries({ queryKey: ["configured-import-options"] });
                void sourceQuery.refetch();
                void queryClient.invalidateQueries({ queryKey: ["keys"] });
                void queryClient.invalidateQueries({ queryKey: ["catalog"] });
                void queryClient.invalidateQueries({ queryKey: ["metrics"] });
              }}
            />
          ) : noSources && (channelView || view === "balances" || view === "overview") ? (
            <section className="data-panel">
              <div className="data-heading"><h2>{routeView ? "路由顺序" : channelView ? "渠道观测" : view === "balances" ? "余额管理" : "总览"}</h2></div>
              <Empty title="还没有来源" icon={<Server size={22} />}>
                <button className="button primary" onClick={() => selectView("sources")}>添加来源</button>
              </Empty>
            </section>
          ) : view === "automations" && baseConnection.account ? (
            <Automation sources={sourceList} models={models} />
          ) : view === "prices" ? (
            <PriceSettings
              prices={prices.data?.data || []}
              drafts={priceDrafts}
              onDraftChange={setPriceDrafts}
              pendingModels={savingPrices}
              onPendingChange={setSavingPrices}
              refreshAction={refreshButton}
              loading={prices.isPending}
              error={prices.error?.message}
              connection={connection}
              onSaved={(price) => {
                queryClient.setQueryData<{ data: ModelPrice[] }>(["prices", connection.session], old => ({
                  data: [...(old?.data || []).filter(item => item.model !== price.model), price],
                }));
                void prices.refetch();
                void queryClient.invalidateQueries({ queryKey: ["metrics"] });
              }}
            />
          ) : view === "overview" && ((!metrics.data && (metrics.isPending || historyInitializing)) || (!catalog.data && catalog.isPending)) ? (
            <PageLoading view="overview" />
          ) : view === "overview" ? (
            <Overview
              metrics={metrics.data}
              rows={rows}
              live={liveMap}
              liveError={liveMetrics.isError}
              livePending={liveMetrics.isPending}
              catalogReady={!!catalog.data && !catalog.isError && !catalog.data.unavailable_sources?.length}
              historyError={metrics.error?.message}
              onLogs={openRelatedLogs}
              onChannel={row => {
                const next = {...filters};
                setPageFilters(current => ({...current, channels:next}));
                history.pushState({workspaceOwner:preferenceScope}, "", workspaceHash("channels",next,rowId(row)));
                setView("channels");
                setPage(0);
                setDetailId(rowId(row));
              }}
            />
          ) : null}
          {!noSources && (channelView || view === "balances") ? (
            <section className="data-panel channel-browser" data-channel-data-state={channelView ? (channelDataComplete ? "complete" : "loading") : undefined}>
              <div className="data-heading workbench-heading" ref={observeHeading}>
                <div className="data-title">
                  <h2>{routeView ? "路由顺序" : channelView ? "渠道观测" : "余额管理"}</h2>
                  <WorkbenchNotice urgent={!!routeIssues || !!error && rows.length > 0} messages={[...observationMessages, routeIssues]} />
                  {channelView && <span className="sr-only" aria-label="排序">{previewingSort ? "排序预览 · 未应用到路由" : keyId ? "按当前 API key 的请求顺序" : sortRules.length ? "按自定义规则" : "按渠道配置顺序"}</span>}
                </div>
                <div className="workbench-tools">
                  {routeView && <button type="button" className="button small ghost" onClick={() => selectView("sub2api")}><ChevronLeft size={14} />渠道管理</button>}
                  <div className="workbench-time-slot">{adjustingChannels && !inlineRoute ? routeActions : timeFilter}</div>
                  {inlineModel && modelFilter}
                  {inlineKey && apiKeyFilter}
                  {inlineRoute && <div className="route-edit-slot">{adjustingChannels ? routeActions : routeButton}</div>}
                  {view === "balances" && <WorkbenchDisclosure label="检查余额" icon={<Wallet size={14} aria-hidden="true" />}>
                    <button type="button" className="button small" disabled={!activeAudit && (!balanceCatalog.length || busy || search !== deferredSearch)} onClick={event => {
                      if (activeAudit) setBalanceAudit(null);
                      else setBalanceAudit({scope:auditScope, targets:[...balanceCatalog]});
                      const menu = event.currentTarget.closest("details");
                      if (menu) { menu.open = false; menu.querySelector("summary")?.focus(); }
                    }}>{activeAudit ? "停止检查" : `检查 ${count(balanceCatalog.length)} 个渠道`}</button>
                  </WorkbenchDisclosure>}
                  <WorkbenchDisclosure key={view} className="channel-filter-disclosure" label={`筛选${activeFilters.length ? ` ${activeFilters.length}` : ""}`} icon={<Filter size={14} aria-hidden="true" />}
                    title={[search, model, selectedSourceId && sourceList.find(source => source.id === selectedSourceId)?.name, statusFilter === "eligible" ? "可用" : statusFilter ? "不可用" : ""].filter(Boolean).join(" · ") || undefined}>
                <WorkbenchTabs label="筛选面板" value={filterTab} onChange={setFilterTab}
                  tabs={[["conditions","条件"],["display","显示"],["tools","工具"]]} />
                <section hidden={filterTab !== "conditions"} aria-label="筛选条件">
                <div className="data-actions" id="channel-workspace-actions">
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
                </div>
                  <div className="channel-filter-grid">
                {adjustingChannels && !inlineRoute && <div className="channel-filter-field"><span>时间</span>{timeFilter}</div>}
                {sourceList.length > 0 && (
                  <label className="channel-filter-field">来源<span className="select-field">
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
                  </span></label>
                )}
                {!inlineKey && <div className="channel-filter-field"><span>API key</span>{apiKeyFilter}</div>}
                {!inlineModel && <div className="channel-filter-field"><span>模型</span>{modelFilter}</div>}
                {view === "balances" && (
                  <>
                    <label className="channel-filter-field">优先级<span className="select-field">
                      <select
                        aria-label="模型优先级筛选"
                        value={balanceTopN}
                        onChange={(e) => setFilter("balanceTopN", e.target.value)}
                      >
                        <option value="">全部优先级</option>
                        {[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20, 50, 100].map((rank) => (
                          <option key={rank} value={rank}>
                            前 {rank} 名渠道
                          </option>
                        ))}
                      </select>
                      <ChevronDown size={12} />
                    </span></label>
                    <label className="channel-filter-field">
                      余额低于 $
                      <input
                        className="channel-filter-number" aria-label="余额低于美元"
                        type="number"
                        min="0"
                        step="any"
                        aria-invalid={!balanceThresholdValid}
                        placeholder="不限"
                        value={balanceThreshold}
                        onChange={(e) => setFilter("balanceThreshold", e.target.value)}
                      />
                    </label>
                  </>
                )}
                {channelView && (
                  <>
                    <label className="channel-filter-field">端点<span className="select-field">
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
                    </span></label>
                    <label className="channel-filter-field">流式<span className="select-field">
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
                    </span></label>
                    <label className="channel-filter-field">路由状态<span className="select-field">
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
                    </span></label>
                    <label className="channel-filter-field">失败原因<span className="select-field">
                      <select
                        aria-label="失败原因筛选"
                        title="筛选当前范围内存在此类失败的渠道；成功率仍按全部尝试统计"
                        value={failureReason}
                        onChange={(e) => setFilter("failureReason", e.target.value)}
                      >
                        <option value="">全部失败原因</option>
                        <option value="compaction">压缩失败</option>
                        {[...knownFailures].map(reason => <option key={reason} value={reason}>
                          {failureReasonLabel(reason) === "其他失败" && reason !== "other" ? `其他失败 · ${reason}` : failureReasonLabel(reason)}
                        </option>)}
                      </select>
                      <ChevronDown size={12} />
                    </span></label>
                  </>
                )}
                  </div>
                  <footer className="channel-filter-footer">
                    {channelView && (
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
                    )}
                {hasFilters && (
                  <button
                    className="filter-chip"
                    onClick={() => {
                      setPageFilters((current) => ({
                        ...current,
                        [filterScope]: { ...defaultFilters },
                      }));
                      setPage(0);
                    }}
                  >
                    <X size={12} /> 重置筛选
                  </button>
                )}
                  </footer>
                </section>
                <section hidden={filterTab !== "display"} aria-label="显示设置">
                  {channelView ? <div className="channel-filter-grid">
                    <div className="channel-filter-field"><span>列预设</span>
                    <label className="select-field">
                      <select aria-label="渠道表列布局" value={observationLayout} onChange={e=>{
                        setCustomColumns(null);
                        setColumnView(e.target.value as ChannelMetricLayout);
                      }}>
                        <option value="run">运行列</option><option value="quality">质量列</option>
                        <option value="finance">费用列</option><option value="all">全部列</option>
                        {customColumns && <option value="custom" disabled>自定义列</option>}
                      </select><ChevronDown size={12} />
                    </label>
                    </div>
                    <div className="channel-filter-field"><span>排序</span>
                  {channelView && <ChannelSortDialog key={channelScope} rules={sortRules}
                    previewRows={rules=>{const visible=new Set(filtered.map(rowId));return sortChannels(rows.filter(row=>visible.has(rowId(row))),rules,{checks:checks.results,multipliers:imported.data?.multipliers});}}
                    onPreview={preview=>{setSortPreview(preview?{scope:channelScope,rows:preview}:null);setPage(0);}}
                    labelRows={rows}
                    routing={routeView?routingSort:undefined}
                    keyId={keyId}
                    routeApplyDisabled={busy||!!error||adjustingChannels||controls.pending||controls.drafts.size>0||!!(balanceFilter&&pendingBalances)||search!==deferredSearch}
                    scopeLabel={[
                      model || "全部模型",
                      sourceList.find(s=>s.id===selectedSourceId)?.name || "全部来源",
                      keyId ? [keys.data?.data.find(k=>k.key_id===keyId)?.source_name, `Key ${keys.data?.data.find(k=>k.key_id===keyId)?.position || "（已选）"}`].filter(Boolean).join(" · ") : "全部 API key",
                      ranges.find(([value])=>value===window)?.[1] || window,
                      endpoint === "all" ? "全部端点" : endpoint,
                      stream === "all" ? "全部流式状态" : stream === "true" ? "流式" : "非流式",
                      statusFilter ? statusFilter === "eligible" ? "可用渠道" : "不可用渠道" : "全部状态",
                      ...(balanceFilter ? ["余额不足"] : []),
                      ...(failureReason ? [`包含${failureReason === "compaction" ? "压缩失败" : failureReasonLabel(failureReason)}`] : []),
                      ...(search ? [`搜索：${search}`] : []),
                    ].join(" · ")}
                    templates={sortPreferences.templates}
                    templateId={channelSort.templateId}
                    suggestedTemplateId={!Object.hasOwn(sortPreferences.scopes,channelScope) ? sortPreferences.modelTemplates[model] : undefined}
                    onSaveTemplate={(name,rules) => {
                      const id=crypto.randomUUID();
                      setSortPreferences(current=>({...current,templates:[...current.templates,{id,name,rules:rules.map(r=>({...r}))}]}));
                      return id;
                    }}
                    onRenameTemplate={(id,name)=>setSortPreferences(current=>({...current,templates:current.templates.map(t=>t.id===id?{...t,name}:t)}))}
                    onDeleteTemplate={id=>setSortPreferences(current=>deleteSortTemplate(current,id))}
                    onApply={(rules,templateId) => {
                    setPage(0);
                    setSortPreferences(current=>applyScopedChannelSort(current,channelScope,model,{sort:rules.length?"custom":"config",rules,templateId}));
                  }} />}
                    </div>
                  </div> :
                    <label className="channel-filter-checkbox"><input type="checkbox" checked={balanceDetails} onChange={event => setBalanceDetails(event.target.checked)} /> 性能与费用列</label>
                  }
                {channelView && <TableColumns choices={columnChoices} value={visibleColumns} onChange={setCustomColumns}
                  onReset={() => { setCustomColumns(null); setColumnView("run"); }} />}
                </section>
                <section hidden={filterTab !== "tools"} aria-label="渠道工具">
                  <div className="channel-filter-toolbox">
                  {routeView && routingSort.pending && <button className="button small" title="恢复上一次应用前的路由顺序" disabled={routingSort.busy} onClick={()=>void routingSort.undo()}>撤回排序</button>}
                  {routeView && routingSort.error && <WorkbenchNotice label="排序操作失败" messages={[routingSort.error]} />}
                  {canEditRoutes && !inlineRoute && !adjustingChannels && routeButton}
                  {adjustingChannels && <ChannelControlReset controls={controls} sources={sourceList} keys={keys.data?.data || []} />}
                  {adjustingChannels && hasRouteDrafts && <button type="button" className="button small"
                    aria-label="放弃全部临时修改" disabled={controls.pending} onClick={controls.discardAll}>放弃草稿</button>}
                  {channelView && (
                    <ObservedDetectionActions
                      checks={checks}
                      detection={observedDetection}
                      disabled={
                        !baseConnection.account ||
                        busy ||
                        !!error ||
                        search !== deferredSearch ||
                        !!(balanceFilter && pendingBalances)
                      }
                    />
                  )}
                  {view === "balances" && <button className="button small" onClick={refreshBalances}><Wallet size={14}/> 刷新已查余额</button>}
                  {channelView && (
                    <button
                      className={`button small ghost ${showTrend ? "selected" : ""}`}
                      aria-expanded={showTrend}
                      aria-controls="channel-trend"
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
                  </div>
                {!!keys.data?.data.length && <details className="workbench-key-rates">
                  <summary>调用 Key 成功率</summary>
                  <dl>{keys.data.data.map(key => <div key={key.key_id}>
                    <dt>{key.source_name ? `${key.source_name} · ` : ""}Key {key.position} · {key.prefix}</dt>
                    <dd>{keyRequestStats.label(key)}</dd>
                  </div>)}</dl>
                </details>}
                </section>
                  </WorkbenchDisclosure>
                </div>
              </div>
              {adjustingChannels && <span id={routeHelpId} className="sr-only">在当前页的同一来源内拖动渠道。键盘可用上下方向键移动，Home 置顶。同一渠道的所有模型一起移动。</span>}
              {view === "balances" && <WorkbenchTabs label="余额视图" value={balanceView} onChange={setBalanceView} tabs={[["wallets","站点钱包"],["channels","关联渠道"]]} />}
              {view === "balances" && balanceView === "wallets" ? <section className="wallet-summary" aria-label="站点钱包">
                {groupedWallets.size === 0 && <p>{balanceLoading ? "正在查询站点关联" : "当前范围暂无已核验站点账号"}</p>}
                <ul>{[...groupedWallets].map(([id,entry])=><li key={id}>
                  <strong>{entry.label}</strong><span>{hasWalletAmount(entry.wallet) && <BalanceAmount value={entry.wallet.amount} />}
                    {(entry.wallet.status !== "ok" || !hasWalletAmount(entry.wallet)) && <> {walletStatusLabel(entry.wallet)}</>}
                    {" · "}关联 {entry.providers.size} 个渠道</span>
                  <button type="button" className="button small" onClick={()=>setWalletDialog(id)}>充值设置与订单</button>
                </li>)}</ul>
              </section> : null}
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
              {error && !rows.length ? (
                <div role="alert" className="table-error">
                  <Unplug size={26} />
                  <h3>暂时无法读取渠道</h3>
                  <p>{error}</p>
                  <button className="button" onClick={reload}>
                    重新读取
                  </button>
                </div>
              ) : catalog.isPending || metrics.isPending || (historyInitializing && !metrics.data) ? (
                <TableLoading kind={channelView ? "channels" : "balances"} observation={channelView ? {
                  header: observationHeader, layout: observationLayout, compact: compactObservation,
                  classes:[...(routeView ? ["rank"] : []),"channel-identity",...(checkingChannels ? [""] : []),...visibleColumns.map(id => observationColumnClass[id])],
                } : undefined} />
              ) : total === 0 ? (
                <Empty
                  title={
                    pendingBalances &&
                    (channelView ? balanceFilter : balanceThreshold)
                      ? "正在确认渠道余额"
                      : "没有匹配的渠道"
                  }
                  icon={<Search size={26} />}
                >
                  {pendingBalances &&
                  (channelView ? balanceFilter : balanceThreshold)
                    ? `还有 ${pendingBalances} 个渠道正在查询，结果到达后会自动显示。`
                    : "尝试调整模型、优先级或余额条件，或清除搜索条件。"}
                </Empty>
              ) : channelView ? (
                <div className="table-scroll" tabIndex={0} role="region" aria-label="渠道观测表">
                  <table className="channel-table observation-table" data-columns={observationLayout} data-compact={compactObservation || undefined}
                    style={observationLayout === "custom" ? {minWidth: `max(100%, ${170 + 110 * visibleColumns.length}px)`} : undefined}>
                    <thead>
                      <tr>{observationHeader}</tr>
                    </thead>
                    <tbody>
                      {pageRows.map((row) => {
                        const balance = balanceMap.get(providerId(row));
                        const historicalCost = historicalCostMap.get(providerId(row));
                        const control = adjustingChannels ? controls.info(row) : null;
                        const routeDisabled = !control?.ready || control.pending || control.stale;
                        const routeLabel = `${row.source_name || row.source_id || ""} ${channelName(row)} ${row.model}`.trim();
                        return (
                          <tr key={rowId(row)} data-route-row={!routeDisabled ? rowId(row) : undefined}
                            data-route-alert={!row.eligible || undefined} data-source-name={row.source_name}>
                            {routeView && <td className="rank mono">
                              {String(rowRanks.get(rowId(row))).padStart(
                                2,
                                "0",
                              )}
                            </td>}
                            <td className="channel-identity"><div className="channel-row-identity">
                              {control && <button type="button" className="icon-button route-grip" aria-label={`移动 ${routeLabel}`}
                                aria-describedby={routeHelpId} aria-keyshortcuts="ArrowUp ArrowDown Home" disabled={routeDisabled}
                                title={control.error || (control.stale ? "规则已变化，请先放弃草稿" : !control.ready ? "暂不可调整" : "拖动排序（也可用上下方向键）")}
                                {...routeDrag(row)} onKeyDown={event => {
                                  const direction = event.key === "ArrowUp" ? -1 : event.key === "ArrowDown" ? 1 : 0;
                                  if (!direction && event.key !== "Home") return;
                                  event.preventDefault();
                                  const handle = event.currentTarget;
                                  if (direction) controls.move(row, direction, pageRows);
                                  else controls.moveToTop(row);
                                  requestAnimationFrame(() => handle.focus({preventScroll:true}));
                                }}><GripVertical size={16} aria-hidden="true" /></button>}
                              <button
                                className="channel-link"
                                aria-label={`查看 ${channelName(row)} ${row.model} 详情`}
                                onClick={() => openDetail(rowId(row))}
                              >
                                <span>
                                  <strong title={[channelName(row), channelBillingHint(row)].filter(Boolean).join(" · ")}>{channelName(row)}</strong>
                                  {(!model || row.source_name && !selectedSourceId) && <small>{!model && row.model}{!model && row.source_name && !selectedSourceId && " · "}{!selectedSourceId && row.source_name && <span className="source-label">{row.source_name}</span>}</small>}
                                </span>
                              </button>
                              {control && <ChannelRowActions row={row} controls={controls} state={control} disabled={routeDisabled} />}
                            </div></td>
                            {checkingChannels && (observedDetection.project === "quality"
                              ? <ChannelCheckAction row={row} checks={checks} />
                              : <ObservedDetectionRow row={row} detection={observedDetection} />)}
                            <ChannelMetricCells
                              row={row}
                              inflight={liveMap.get(rowId(row))}
                              balance={balance}
                              balanceLoading={balanceLoading}
                              actualRange={actualRange}
                              historicalCost={historicalCost && { amount: historicalCost.data, pending: historicalCost.isPending }}
                              spend={baseConnection.account ? channelSpend.get(rowId(row)) : undefined}
                              importedChannel={!!baseConnection.account && row.provider.startsWith("sub2api-")}
                              stale={staleSources.has(row.source_id || "")}
                              metricsUnavailable={!metrics.data}
                              columns={visibleColumns}
                              renderDetection={id => id === "quality" ? <LatestChannelCheck row={row} checks={checks} /> : <td className="column-quality"><FingerprintCell
                                entries={observedFingerprints(row, fingerprints.index, fingerprints.members.get(providerId(row)), fingerprints.supportedModels)}
                                error={fingerprints.error} loading={fingerprints.isPending}
                                supportedModels={fingerprints.supportedModels}
                              /></td>}
                            />
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              ) : balanceView === "channels" ? (
                <div className="table-scroll" tabIndex={0} role="region" aria-label="渠道余额表，可横向滚动">
                  <table className="channel-table balance-table" data-details={balanceDetails || undefined}>
                    <thead>
                      <tr>
                        <th className="channel-identity">渠道</th>
                        {balanceDetails && <>
                        <th>模型 / 优先级</th>
                        <th title="该渠道该模型的成功尝试 ÷（成功 + 失败）；取消和跳过不计入。">渠道成功率</th>
                        <th title="渠道请求开始至首个正文增量的 P50，缺失时显示 —。">首字 P50</th>
                        <th title="当前渠道单次尝试从开始到结束的 P50，包含成功和失败，不包含前序重试。">总耗时 P50</th>
                        <th>估算消费</th>
                        <th>渠道实际消费</th>
                        <th>渠道利润参考</th>
                        </>}
                        <th>站点余额</th>
                        <th>状态</th>
                        <th>最近查询</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pageProviders.map(provider => {
                    const balance = balanceMap.get(provider);
                    const providerName = firstProviders?.filtered.get(provider);
                    const displayName = providerName
                      ? channelName(providerName)
                      : JSON.parse(provider)[1];
                    const sourceRow = firstProviders?.all.get(provider);
                    const sourceName = sourceRow?.source_name;
                    const sourceLink = siteFor(sourceRow) || dashboardURL(sourceList.find((source) => source.id === sourceRow?.source_id)?.base);
                    const ranks = balanceRanks?.ranks.get(provider) || [];
                    const modelRanks = balanceDetails ? ranks : ranks.slice(0,1);
                    const checkedWallets = (balance?.data || []).filter(wallet => wallet.checked_at && wallet.checked_at > 0);
                    let newest = 0, oldest = Infinity;
                    for (const wallet of checkedWallets) {
                      newest = Math.max(newest, wallet.checked_at!);
                      oldest = Math.min(oldest, wallet.checked_at!);
                    }
                    const finance = balanceFinance.get(provider);
                    return (
                      <Fragment key={provider}>{modelRanks.map(({ model: modelName, rank }, modelIndex) => <tr key={modelName} className={modelIndex === 0 ? "balance-provider-start" : "balance-model-continuation"}>
                        {modelIndex === 0 && <td className="channel-identity" rowSpan={modelRanks.length}>
                          <div className="balance-channel">
                            <span>
                              <strong>{providerName ? <button type="button" className="channel-link" onClick={() => openDetail(rowId(providerName))}>{displayName}</button> : <SiteLink base={sourceLink}>{displayName}</SiteLink>}</strong>
                              <small>{sourceName || "uni-api"}</small>
                            </span>
                          </div>
                        </td>}
                        {balanceDetails && <><td>
                          <div className="balance-model-ranks">
                              <span>
                                {modelName}<small>第 {rank} 位</small>
                              </span>
                          </div>
                        </td>
                        <BalanceModelMetricCells
                          value={balanceModelMetrics.byModel.get(balanceModelMetricID(sourceRow?.source_id || "", sourceRow?.provider || JSON.parse(provider)[1], modelName))}
                          pending={balanceModelMetrics.isPending}
                          error={balanceModelMetrics.isError}
                          partial={balanceModelMetrics.data?.import?.caught_up === false}
                        />
                        </>}
                        {modelIndex === 0 && <>
                        {balanceDetails && <>
                        <td rowSpan={modelRanks.length}>
                          <span className={`mono balance-finance ${finance?.estimated == null ? "muted" : ""}`}>
                            {finance?.estimated == null ? "—" : usd(finance.estimated)}
                            {!!finance?.missingPrices && <small className="profit-bound-label">有 {finance.missingPrices} 项用量未定价</small>}
                          </span>
                        </td>
                        <td rowSpan={modelRanks.length}>
                          <span title={finance?.actualExplanation} className={`mono balance-finance ${finance?.actual == null ? "muted" : ""}`}>
                            {finance?.actual == null ? finance?.actualLabel || "—" : `${finance.actualUpperBound ? "≥" : ""}${usd(finance.actual)}`}
                            {finance?.actual != null && finance.provisional && <small className="profit-bound-label">暂计</small>}
                          </span>
                        </td>
                        <td rowSpan={modelRanks.length}>
                          <span title={finance?.actualExplanation} className={`mono balance-finance ${finance?.profit == null ? "muted" : finance.profit < 0 ? "negative" : "profit-positive"}`}>
                            {finance?.profit == null ? "—" : `${finance.actualUpperBound ? "≤" : ""}¥${finance.profit.toFixed(2)}`}
                            {finance?.profit != null && finance.provisional && <small className="profit-bound-label">暂计</small>}
                          </span>
                        </td>
                        </>}
                        <td rowSpan={modelRanks.length}>
                          <BalanceValue
                            balance={balance?.data}
                            loading={balanceLoading}
                          />
                        </td>
                        <td rowSpan={modelRanks.length}>
                          {balanceIsLow(balance?.data) ? (
                            <span className="status-pill cooling">余额不足</span>
                          ) : balanceLoading || balance?.isPending ? (
                            <span className="muted">查询中</span>
                          ) : balance?.data.some(wallet => wallet.status === "error") ? (
                            <span className="status-pill cooling">更新失败</span>
                          ) : balanceIsKnown(balance?.data) ? (
                            <span className="status-pill healthy">已核验</span>
                          ) : (
                            <span className="muted">未核验</span>
                          )}
                        </td>
                        <td rowSpan={modelRanks.length} className="muted">
                          {newest ? <>{newest !== oldest && <small>最早 {dateTime(oldest)}<br /></small>}{dateTime(newest)}
                            {(balance?.data.length ?? 0) > 1 && <><br /><small>{checkedWallets.length} / {balance?.data.length} 个账号</small></>}</> : "暂无"}
                        </td>
                        </>}
                      </tr>)}</Fragment>
                    );
                  })}
                    </tbody>
                  </table>
                </div>
              ) : null}
              <div className="table-footer">
                <span>
                  {error
                    ? "数据不可用"
                    : `${count(total)} ${channelView && !model ? "条渠道 / 模型" : "个渠道"}`}
                  {!error && balanceCatalog.length > 0 && <span className="balance-coverage">余额已核验 {knownBalances}/{balanceCatalog.length}{knownBalances > 0 ? ` · 不足 ${lowCount}` : ""}</span>}
                  {pendingBalances > 0 && (
                    <span className="footer-pending">
                      <Spinner small />
                      余额查询 {balanceQueries.length - pendingBalances}/
                      {balanceQueries.length}
                    </span>
                  )}
                </span>
                <div className="pagination">
                  <button type="button" className="icon-button" aria-label="刷新数据" title="刷新数据" disabled={refreshBusy} onClick={reload}>
                    <RefreshCw size={15} className={refreshBusy ? "spin" : ""} />
                  </button>
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
        </Suspense></PageBoundary></main>
      </div>
      {detail && <PageBoundary key={rowId(detail)} onDismiss={closeDetail}><Detail
        session={baseConnection.session}
        row={detail}
        site={siteFor(detail) || dashboardURL(sourceList.find((source) => source.id === detail?.source_id)?.base)}
        check={detail ? checks.results.get(providerId(detail)) : undefined}
        trend={{ connection, keyId, window, endpoint, stream, refresh }}
        catalog={catalog.data?.data || []}
        imports={baseConnection.account ? imported : undefined}
        onClose={closeDetail}
        onLogs={row => openRelatedLogs(row.model, false, row)}
        balance={detail ? balanceMap.get(providerId(detail)) : undefined}
      /></PageBoundary>}
      <ConsoleLocator open={locator} onOpenChange={open => {
        setLocator(open);
        if (!open) requestAnimationFrame(() => {
          if (!document.querySelector('[role="dialog"]')) locatorOpener.current?.focus({preventScroll:true});
        });
      }} account={!!baseConnection.account}
        channels={locatorCatalog.data?.data || emptyChannels} loading={locatorCatalog.isFetching} failed={locatorCatalog.isError}
        onPage={selectView}
        onChannel={row => {
          detailOpener.current = locatorOpener.current;
          setLocatedChannel(rows.find(candidate => rowId(candidate) === rowId(row)) || row);
        }}
        onRequest={id => {
          const hash = requestLocation({...defaultFilters}, {id,source:""}, null);
          if (view === "requests") {
            history.pushState({workspaceOwner:preferenceScope}, "", hash);
            globalThis.dispatchEvent(new PopStateEvent("popstate", {state:history.state}));
          } else {
            history.pushState({workspaceOwner:preferenceScope}, "", hash);
            setView("requests");
            setDetailId(null);
            setLocatedChannel(null);
          }
        }} />
      <Guide open={guide} onClose={() => setGuide(false)} />
      {walletDialog && <PageBoundary key={walletDialog} onDismiss={()=>setWalletDialog(null)}><Suspense fallback={<div className="balance-dialog-loading" role="status">加载充值设置…</div>}>
        <BalanceWalletDialog key={walletDialog} id={walletDialog} close={()=>setWalletDialog(null)}/>
      </Suspense></PageBoundary>}
    </div>
  );
}
