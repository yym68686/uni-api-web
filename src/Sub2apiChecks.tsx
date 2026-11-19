import { modelProgressDetails, useSubSyncProgress, useSubSyncResultRefresh } from "./subSyncProgress";
import type { SyncProgress } from "./subSyncProgress";
import { fingerprintTargets, fingerprintLabels, fingerprintBusy, useModelFingerprints, FINGERPRINT_BATCH_LIMIT, FingerprintCell, FingerprintPanel, fingerprintNote } from "./ModelFingerprints";
import { fingerprintKey, sharedFingerprintIndex } from "./fingerprintResults";
import { isCapabilityCheckModel, capabilityCheckScope } from "./sub2apiModels";
import { CopyProbeCurl } from "./CopyProbeCurl";
import { TableLoading, DetailLoading } from "./PageLoading";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  ChevronDown,
  CircleHelp,
  Globe2,
  Plus,
  RefreshCw,
  ScanLine,
  Search,
  Settings2,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { ApiError, controlRequest } from "./api";
import { accountBalanceQuery } from "./accountBalance";
import type { AccountBalance as SubAccountBalance } from "./accountBalance";
import { BalanceAmount } from "./BalanceAmount";
import { AccountForm } from "./SiteAccountForm";
import { CompactDialogHeader, RemovalDialog } from "./CompactDialog";
import { useChannelDetection, detectionEnabled } from "./channelDetection";
import { DetectionAction, detectionProjectLabels, detectionProjects, useDetectionProject } from "./DetectionAction";
import { ResponseLatency } from "./LatencyBadge";
import { Empty, Spinner, Tip } from "./ui";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
import { useImportBindings, boundGroups } from "./sub2apiImports";
import { readCompleteSubTarget } from "./subTargetDetails";
import { CreateChannel } from "./CreateChannel";
import { ChannelSettings } from "./ChannelSettings";
import { ChannelOptimization } from "./ChannelOptimization";
import { ChannelProtocolSummary } from "./ChannelProtocolSummary";
import { useManagementPage, readManagementSelection, readManagementOptimizationPreview, managementOptimizationSnapshot, type ManagementNavigation } from "./managementQuery";
import type { ManagementPagePosition, ManagementSelection } from "./managementQuery";
import { channelMembers, managementRows, UNASSIGNED_ACCOUNT } from "./channelManagement";
import type { ManagedChannel, ManagementRow } from "./channelManagement";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import { managedRouteCount } from "./channelRouteData";
import { useConsoleSources } from "./consoleSources";
import { CompactionStatus, compactionLabels } from "./SubCompaction";
import type { CompactionResult } from "./SubCompaction";
import { ToolUseStatus, toolUseLabels, toolUseDescription } from "./SubToolUse";
import type { ToolUseResult } from "./SubToolUse";
import {modelToolUse} from "./toolUse";
import { SUB_MODELS, initializeSubModels, loadSubModels, saveSubModels, subModelSelectionNeedsReview } from "./sub2apiModels";
import { SubModelSettings } from "./SubModelSettings";
import {
  modelChecks,
  groupQualityResult,
  availabilityCounts,
  importModelLabel,
  probeHasWarning,
  modelMatchStatus,
  modelMatchLabels,
  modelDetectionState,
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { dateTime } from "./format";
import "./sub2apiTable.css";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import type { QualitySummary } from "./QualityHistory";
import { SiteLink } from "./ChannelSite";
import { PriceStatus, GroupPriceStatus, UsageDetailRows } from "./Sub2apiPricing";
import type { SubUsage } from "./sub2apiPriceCheck";
import type { ModelPrice } from "./types";
import { BillingRate } from "./BillingRate";
import type { ObservedRate } from "./billingRates";
import { TableColumns, useTableColumns } from "./TableColumns";
import { managementColumnsStorageKey, managementOverviewForColumns } from "./managementOverview";
import { PageBoundary } from "./PageBoundary";
import { RouteWorkspaceLoading } from "./RouteWorkspaceLoading";

const SiteAccountSettings = lazy(() => import("./SiteAccountSettings").then(m => ({default: m.SiteAccountSettings})));
const Sub2apiImport = lazy(() => import("./Sub2apiImport").then(m => ({default: m.Sub2apiImport})));

const managementColumnChoices = [
  {id: "model", label: "模型"},
  {id: "platform", label: "平台 / key"},
  {id: "rate", label: "倍率"},
  {id: "availability", label: "可用性"},
  {id: "latency", label: "首字延迟"},
  {id: "fingerprint", label: "模型指纹"},
  {id: "protocol", label: "协议"},
  {id: "match", label: "模型匹配"},
  {id: "price", label: "单价异常"},
  {id: "quality", label: "Astra 降智"},
  {id: "compaction", label: "远程压缩"},
  {id: "tool-use", label: "Tool use"},
  {id: "diagnostics", label: "回复 / 诊断"},
  {id: "checked", label: "最近检测"},
  {id: "batch", label: "批量检测"},
] as const;
type ManagementColumn = (typeof managementColumnChoices)[number]["id"];
const managementPresets = {
  operations: ["model", "platform", "rate", "availability", "latency", "fingerprint", "checked", "batch"],
  checks: ["model", "platform", "rate", "availability", "latency", "fingerprint", "protocol", "match", "quality", "compaction", "tool-use", "diagnostics", "checked", "batch"],
  finance: ["model", "rate", "price", "availability", "fingerprint", "checked", "batch"],
  all: managementColumnChoices.map(({id}) => id),
} satisfies Record<string, readonly ManagementColumn[]>;
const managementColumnLabels = Object.fromEntries(managementColumnChoices.map(({id,label}) => [id,label])) as Record<ManagementColumn, string>;
const managementColumnHelp: Partial<Record<ManagementColumn, string>> = {
  fingerprint: `点击多模型的指纹状态可查看完整列表。${fingerprintNote}`,
  match: "检测请求与实际返回的模型名完全一致才算匹配。",
  price: "比较倍率前账单单价与价格设置。缺少账单或 Token 样本时不判为正常。",
  "tool-use": toolUseDescription,
  batch: "关闭后跳过批量检测，仍可手动检测。已提交的任务继续执行。",
};

export interface Probe {
  curl_token?: string;
  id?: string;
  started_at?: number;
  request_ids?: string[];
  usage?: SubUsage;
  requested_model?: string;
  response_model?: string;
  model_match?: "match" | "mismatch" | "missing" | "invalid" | "unavailable";
  status: string;
  terminal_status?: "complete" | "missing" | "missing_output";
  text: string;
  message?: string;
  ttft_ms: number | null;
  response_created_ms?: number | null;
  first_response_ms?: number | null;
  protocol?: "responses" | "gemini" | "messages" | "chat";
  duration_ms: number;
  http_status?: number;
}
export interface Result {
  model: string;
  checked_at: number;
  availability: Probe;
  quality: Probe;
  verdict: string;
}
export interface SubTarget {
  rate_coefficient?: number;
  summary_model?: string;
  list_summary?: boolean;
  list_overview?: Omit<ReturnType<typeof availabilityCounts>, "rejected" | "skipped"> & { total: number; checked_at: number; rejected?: number; skipped?: number };
  receipt_model?: string;
  observed_rates?: Record<string, ObservedRate>;
  details_omitted?: boolean;
  check_source_id?: string;
  tool_use?: ToolUseResult;
  tool_use_state?: string;
  compaction?: CompactionResult;
  compaction_state?: string;
  quality_check?: import("./ChannelChecks").ChannelCheck & { quality_probe?: Probe };
  history?: QualitySummary;
  models?: {
    model: string;
    source_name?: string;
    state: string;
    message: string;
    result: Result | null;
  }[];
  group_id: number;
  name: string;
  platform: string;
  channel: string;
  rate: number;
  billing?: { rate: number | null; source: string; checked_at: number } | null;
  key_id: number;
  active: boolean;
  state: string;
  message: string;
  result: Result | null;
}
export { groupQualityResult } from "./sub2apiResults";
export interface SubAccount {
  rate_coefficient?: number;
  active_groups?: number;
  completed_groups?: number;
  id: string;
  name: string;
  base: string;
  email: string;
  login_name?: string;
  provider_kind?: string;
  has_saved_password?: boolean;
  state: string;
  job_kind?: string;
  sync_models?: string[];
  sync_only?: boolean;
  sync_phase?: string;
  sync_retry_at?: number;
  probe_limits?: {rpm_limit?: number; concurrency?: number; declared_rpm?: number; declared_concurrency?: number; effective_rpm?: number; effective_concurrency?: number; source?: string};
  progress?: SyncProgress;
  message: string;
  synced_at: number;
  balance?: SubAccountBalance | null;
  targets: SubTarget[];
}
const pending = (state: string) => state === "queued" || state === "running";
const syncPhaseLabel: Record<string,string> = {
  discovering:"正在读取分组和模型", preparing:"正在准备测试密钥", probing:"检测中",
  waiting_keys:"上游限制密钥读取", waiting_key_create:"等待创建测试密钥", waiting_models:"上游限制模型请求",
  handoff:"等待新实例接续", compaction:"正在检测上下文压缩", tool_use:"正在检测工具调用",
};
const stateLabel: Record<string, string> = {
  idle: "已同步",
  queued: "等待检测",
  running: "检测中",
  stopped: "已停止",
  interrupted: "已中断",
  error: "同步失败",
};
const latency = (value: number | null) =>
  value === null
    ? "—"
    : value < 1000
      ? `${value} ms`
      : `${(value / 1000).toFixed(2)} s`;

function AccountBalance({ account, enabled, user }: { account: SubAccount; enabled: boolean; user: string }) {
  const query = useQuery({
    ...accountBalanceQuery(user, account.id, account.base),
    // This header was read in the just-verified management transaction. Do
    // not issue an identical balance GET for every account at first paint.
    // Missing values still read once; page actions do not restart this query.
    initialData: account.balance
      ? { ...account.balance, provider_kind: account.provider_kind }
      : undefined,
    initialDataUpdatedAt: Date.now(),
    enabled,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const balance = account.balance && account.balance.checked_at > (query.data?.checked_at || 0)
    ? account.balance : query.data || account.balance;
  return (
    <div
      className="sub-account-balance"
      aria-label="账号余额"
      title={
        balance?.checked_at
          ? `余额查询于 ${dateTime(balance.checked_at)}${query.isError || balance.status === "error" ? " · 更新失败，上次结果" : ""}`
          : "尚无余额数据"
      }
    >
      <BalanceAmount value={balance?.amount} />
      {query.isPending && !balance && <Spinner small />}
      {(query.isError ||
        (balance &&
          balance.status !== "ok" &&
          balance.status !== "missing")) && (
        <small>更新失败{balance?.amount != null && " · 上次结果"}</small>
      )}
    </div>
  );
}


function Verdict({ result, history }: { result: Result | null; history?: QualitySummary }) {
  if (result?.verdict === "not_applicable")
    return <span className="muted">不适用</span>;
  if (!result) return <span className="muted">未检测</span>;
  const pass = result.verdict === "pass",
    fail = result.verdict === "fail";
  const status = (
    <span className={`check-status ${pass ? "pass" : fail ? "fail" : "inconclusive"}`}>
      {pass ? <Check size={16} /> : fail ? <X size={16} /> : <CircleHelp size={16} />}
      {pass ? "不降智" : fail ? "降智" : result.quality.status === "skipped" ? "未检测" : result.quality.status === "error" ? "检测失败" : "无法判定"}
    </span>
  );
  return history && history.total > 0 ? <Tip text={qualityTooltip(history, result.checked_at)}>{status}</Tip> : status;
}

function AvailabilityStatus({ check }: { check: SubModelCheck }) {
  const state = modelDetectionState(check);
  if (pending(state))
    return (
      <span className="check-status">
        <Spinner small />
        {check.state === "queued" ? "排队中" : "检测中"}
      </span>
    );
  if (state === "rejected" || state === "skipped")
    return <span className="check-status inconclusive" title={check.message}>{importModelLabel(check)}</span>;
  if (state === "interrupted" || state === "error")
    return (
      <span className="check-status error">
        {check.state === "interrupted" ? "已中断" : "检测失败"}
      </span>
    );
  if (!check.result) return <span className="muted">未检测</span>;
  const success = check.result.availability.status === "success";
  const warning = probeHasWarning(check.result.availability);
  return (
    <span className={`check-status ${warning ? "warning" : success ? "pass" : "fail"}`}>
      {success ? <Check size={15} /> : <X size={15} />}
      {importModelLabel(check)}
    </span>
  );
}

function GroupAvailability({ checks, scope, overview }: { checks: SubModelCheck[]; scope?: string; overview?: SubTarget["list_overview"] }) {
  const total = overview?.total ?? checks.length;
  if (!total) return <span className="muted">未检测</span>;
  const counts = overview ?? availabilityCounts(checks);
  const details = [
    scope,
    counts.warning && `${counts.warning} 个结束异常`,
    counts.failed && `${counts.failed} 个失败`,
    counts.untested && `${counts.untested} 个未检测`,
    counts.pending && `${counts.pending} 个排队 / 检测中`,
    !!counts.rejected && `${counts.rejected} 个上游拒绝`,
    !!counts.skipped && `${counts.skipped} 个已跳过`,
    counts.interrupted && `${counts.interrupted} 个中断`,
  ].filter(Boolean).join(" · ");
  const summary = <span className={`check-status ${counts.success ? "pass" : "inconclusive"}`}>
    {counts.pending > 0 && <Spinner small />}
    {counts.success}/{total} 可用
  </span>;
  return details ? <Tip text={details}>{summary}</Tip> : summary;
}

function ModelMatch({ check }: { check: SubModelCheck }) {
  const state = modelDetectionState(check);
  if (pending(state) || state === "interrupted" || state === "rejected" || state === "skipped") return <span className="check-status" title={check.message}>{importModelLabel(check)}</span>;
  const status = modelMatchStatus(check);
  const label = modelMatchLabels[status];
  const probe = check.result?.availability;
  return (
    <span
      className={`check-status ${status === "match" ? "pass" : status === "mismatch" ? "fail" : "inconclusive"}`}
      title={`请求模型：${probe?.requested_model || check.model}\n返回模型：${probe?.response_model || "—"}`}
    >
      {status === "match" ? (
        <Check size={15} />
      ) : status === "mismatch" ? (
        <X size={15} />
      ) : (
        <CircleHelp size={15} />
      )}
      {label}
    </span>
  );
}

function GroupModelMatch({ checks, scope }: { checks: SubModelCheck[]; scope?: string }) {
  const counts = new Map<string, number>();
  let working = 0, interrupted = 0, rejected = 0, skipped = 0;
  for (const check of checks) {
    const state = modelDetectionState(check);
    if (pending(state)) working++;
    else if (state === "interrupted") interrupted++;
    else if (state === "rejected") rejected++;
    else if (state === "skipped") skipped++;
    else {
      const label = modelMatchLabels[modelMatchStatus(check)];
      counts.set(label, (counts.get(label) || 0) + 1);
    }
  }
  const matches = counts.get(modelMatchLabels.match) || 0;
  const details = [
    scope,
    ...[...counts].filter(([label]) => label !== modelMatchLabels.match).map(([label, count]) => `${count} 个${label}`),
    working && `${working} 个排队 / 检测中`,
    rejected && `${rejected} 个上游拒绝`,
    skipped && `${skipped} 个已跳过`,
    interrupted && `${interrupted} 个中断`,
  ].filter(Boolean).join(" · ");
  const summary = <span className={`check-status ${matches ? "pass" : "inconclusive"}`}>{matches}/{checks.length} 匹配</span>;
  return details ? <Tip text={details}>{summary}</Tip> : summary;
}

function DetailRow({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <tr>
      <th scope="row">{label}</th>
      <td>{children}</td>
    </tr>
  );
}

async function readChannelCheckDetails(account: SubAccount, target: SubTarget, configured: ManagedChannel | undefined, model: string, signal: AbortSignal) {
  if (configured) {
    const members = channelMembers(configured);
    const paths = [...new Set(members.flatMap(member => boundGroups(member).map(group =>
      `/v1/sub2api/accounts/${encodeURIComponent(group.account_id)}/groups/${group.group_id}/details?model=${encodeURIComponent(member.model_mappings?.[model] || model)}`)))];
    const [bound, native] = await Promise.all([
      Promise.all(paths.map(path => controlRequest<{data:SubAccount[]}>(path, {signal}))),
      Promise.all(members.map(m => controlRequest<{data: import("./channelManagement").ConfiguredCheck[]}>(`/v1/channel-management/checks?source_id=${encodeURIComponent(m.source_id)}&provider=${encodeURIComponent(m.provider)}&model=${encodeURIComponent(model)}`, {signal}))),
    ]);
    const accounts = new Map<string, SubAccount>();
    for (const item of bound.flatMap(r=>r.data)) {
      const previous = accounts.get(item.id);
      accounts.set(item.id, {...item, targets:[...(previous?.targets || []), ...item.targets]});
    }
    const full = managementRows([...accounts.values()], [configured], [], model, "", native.flatMap(n=>n.data)).find(row=>row.configured);
    if (!full) throw Error("检测详情暂不可用");
    return full;
  }
  const data = await controlRequest<{data:SubAccount[]}>(`/v1/sub2api/accounts/${encodeURIComponent(account.id)}/groups/${target.group_id}/details?model=${encodeURIComponent(model)}`, {signal});
  const fullTarget = data.data.find(a=>a.id===account.id)?.targets.find(t=>t.group_id===target.group_id);
  if (!fullTarget) throw Error("检测详情暂不可用");
  const checks = modelChecks(fullTarget);
  return {target:fullTarget,checks,selected:checks.find(c=>c.model===model)};
}

function CheckDetails({
  account,
  target,
  checks,
  selected,
  prices,
  configured,
}: {
  account: SubAccount;
  target: SubTarget;
  checks: SubModelCheck[];
  selected?: SubModelCheck;
  prices?: ModelPrice[];
  configured?: ManagedChannel;
}) {
  const [open, setOpen] = useState(false);
  const [detailModel, setDetailModel] = useState(checks[0]?.model || "");
  const model = selected?.model || detailModel;
  const needsDetails = target.details_omitted;
  const modelOptions = checks;
  const fixedModel = !!selected;
  const details = useQuery({
    queryKey: ["sub-check-detail", account.id, target.group_id, configured?.source_id, configured?.provider, model, selected?.result?.checked_at],
    enabled: open && !!needsDetails,
    queryFn: ({ signal }) => readChannelCheckDetails(account, target, configured, model, signal),
    retry: false,
    staleTime: 30_000,
  });
  if (!checks.length) return <span>暂无模型</span>;
  if (details.data) { target = details.data.target; checks = details.data.checks; selected = details.data.selected; }
  const check =
    selected || checks.find((item) => item.model === detailModel) || checks[0];
  const result = check.result;
  const probe = result?.availability;
  const qualityResult = groupQualityResult(target);
  const toolResult = modelToolUse(target,check.model);
  const capabilitiesApplicable = isCapabilityCheckModel(check.model);
  const compactionResult = capabilitiesApplicable && (!target.compaction?.model || isCapabilityCheckModel(target.compaction.model)) ? target.compaction : undefined;
  const curlTargets = [
    ...(account.id ? [{ account_id: account.id, group_id: target.group_id }] : []),
    ...(configured ? channelMembers(configured).flatMap(member => [
      { source_id: member.source_id, provider: member.provider },
      ...boundGroups(member).map(group => ({ account_id: group.account_id, group_id: group.group_id })),
    ]) : []),
    ...(target.quality_check?.source_id ? [{ source_id: target.quality_check.source_id, provider: target.quality_check.provider }] : []),
  ];
  const copyProbe = (kind: "availability" | "quality" | "tool-use" | "compaction", probe: Probe | undefined, label: string) =>
    <CopyProbeCurl probe={probe} kind={kind} targets={curlTargets} label={label} />;
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button
          className="button small"
          aria-label={`查看 ${account.name} ${target.name} 的回复与诊断`}
        >
          查看详情
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog sub-check-dialog">
          <Dialog.Title>回复 / 诊断</Dialog.Title>
          <Dialog.Description>
            {account.name} / {target.name}{account.id ? ` · 分组 #${target.group_id}` : " · uni-api 渠道定向检测"}
          </Dialog.Description>
          <Dialog.Close asChild>
            <button
              className="icon-button detail-close"
              aria-label="关闭检测详情"
            >
              <X size={18} />
            </button>
          </Dialog.Close>
          {!fixedModel && (
            <label className="sub-import-field">
              查看模型
              <select
                value={detailModel}
                onChange={(e) => setDetailModel(e.target.value)}
              >
                {modelOptions.map((item) => (
                  <option key={item.model} value={item.model}>
                    {item.model}
                  </option>
                ))}
              </select>
            </label>
          )}
          {needsDetails && details.isPending ? <DetailLoading kind="form" label="检测详情" /> : details.isError ? <div role="alert">{details.error.message}<button className="button" onClick={()=>void details.refetch()}>重试</button></div> : <table
            className="sub-check-details-table"
            aria-label={`${check.model} 检测详情`}
          >
            <thead>
              <tr>
                <th scope="col">字段</th>
                <th scope="col">检测结果</th>
              </tr>
            </thead>
            <tbody>
              {check.source_name && <DetailRow label="检测来源">{check.source_name}</DetailRow>}
              <DetailRow label="Tool use"><ToolUseStatus target={target} model={check.model} />{capabilitiesApplicable && !toolResult?.attempts.length && copyProbe("tool-use", undefined, "Tool use 检测")}</DetailRow>
              {toolResult?.model && <DetailRow label="工具检测模型">{toolResult.model}</DetailRow>}
              {toolResult?.message && <DetailRow label="工具检测诊断">{toolResult.message}</DetailRow>}
              {toolResult?.attempts.map((attempt, i) => <DetailRow key={attempt.id || i} label="工具检测请求">
                <table className="sub-check-details-table"><tbody>
                  <tr><th>请求模型</th><td>{attempt.requested_model}{copyProbe("tool-use", attempt, `Tool use 检测 ${attempt.requested_model} `)}</td></tr>
                  <tr><th>返回模型</th><td>{attempt.response_model || "—"}</td></tr>
                  <tr><th>HTTP 状态</th><td>{attempt.http_status || "—"}</td></tr>
                  <tr><th>工具调用结果</th><td>{attempt.text || attempt.message || "—"}</td></tr>
                  <tr><th>耗时</th><td>{latency(attempt.duration_ms)}</td></tr>
                  <UsageDetailRows probe={attempt} label="工具检测" prices={prices} model={attempt.requested_model || toolResult?.model || ""} coefficient={target.rate_coefficient} />
                </tbody></table>
              </DetailRow>)}
              <DetailRow label="远程压缩"><CompactionStatus target={target} model={check.model} />{capabilitiesApplicable && !compactionResult?.attempts.length && copyProbe("compaction", undefined, "远程压缩检测")}</DetailRow>
              {compactionResult?.model && <DetailRow label="压缩支持模型">{compactionResult.model}</DetailRow>}
              {compactionResult?.message && <DetailRow label="压缩诊断">{compactionResult.message}</DetailRow>}
              {!!compactionResult?.attempts.length && <DetailRow label="压缩检测请求">
                <table className="sub-check-details-table"><thead><tr><th>模型</th><th>结果 / HTTP</th><th>诊断</th><th>实际扣费</th></tr></thead><tbody>
                  {compactionResult.attempts.filter(attempt => isCapabilityCheckModel(attempt.requested_model || "")).map((attempt, i) => <tr key={attempt.id || i}>
                    <td>{attempt.requested_model}{copyProbe("compaction", attempt, `远程压缩检测 ${attempt.requested_model} `)}</td>
                    <td>{compactionLabels[attempt.status as keyof typeof compactionLabels] || "检测失败"} · {attempt.http_status || "—"}</td>
                    <td>{attempt.message}</td>
                    <td>{attempt.usage?.actual_cost != null ? `$${attempt.usage.actual_cost}` : "—"}</td>
                  </tr>)}
                </tbody></table>
              </DetailRow>}
              <DetailRow label="请求模型">
                <span className="mono">
                  {probe?.requested_model || check.model}
                </span>
              </DetailRow>
              <DetailRow label="返回模型">
                <span className="mono">{probe?.response_model || "—"}</span>
              </DetailRow>
              <DetailRow label="可用性">
                <AvailabilityStatus check={check} />
                {copyProbe("availability", probe, "可用性检测")}
              </DetailRow>
              <DetailRow label="模型匹配">
                <ModelMatch check={check} />
              </DetailRow>
              <DetailRow label="单价核验">
                <PriceStatus check={check} prices={prices} />
              </DetailRow>
              <UsageDetailRows probe={probe} label="可用性" prices={prices} model={check.model} coefficient={target.rate_coefficient} />
              <DetailRow label="最近检测">
                {result ? dateTime(result.checked_at) : "未检测"}
                {result && pending(check.state) && " · 上次结果"}
              </DetailRow>
              <DetailRow label="首字延迟">
                <ResponseLatency
                  created={probe?.response_created_ms}
                  text={probe?.ttft_ms}
                  protocol={probe?.protocol}
                  firstResponse={probe?.first_response_ms}
                />
              </DetailRow>
              <DetailRow label="首个文本延迟">
                {latency(probe?.ttft_ms ?? null)}
                <small className="check-source">
                  {probe?.protocol === "gemini" ? "首个 Gemini 非思考文本片段" : probe?.protocol === "messages" ? "首个 Messages 文本片段" : "首个 response.output_text.delta"}
                </small>
              </DetailRow>
              <DetailRow label="可用性耗时">
                {latency(probe?.duration_ms ?? null)}
              </DetailRow>
              <DetailRow label="HTTP 状态">
                {probe?.http_status || "—"}
              </DetailRow>
              <DetailRow label="可用性回复">
                <div className="sub-check-reply">{probe?.text || "—"}</div>
              </DetailRow>
              {probe?.message && (
                <DetailRow label="可用性诊断">
                  <div className="sub-check-reply">{probe.message}</div>
                </DetailRow>
              )}
              {check.model === "gpt-6-astra" && (
                <>
                  <DetailRow label="Astra 降智">
                    <div className="quality-status-stack"><Verdict result={qualityResult} history={target.history} /><QualityProbability history={target.history} checkedAt={qualityResult?.checked_at} /></div>
                    {copyProbe("quality", qualityResult?.quality, "降智检测")}
                  </DetailRow>
                  {qualityResult?.quality.id && qualityResult.quality.id === probe?.id ? (
                    <DetailRow label="降智扣费">与可用性为同一次请求，扣费见上方</DetailRow>
                  ) : <UsageDetailRows probe={qualityResult?.quality} label="降智" prices={prices} model={check.model} coefficient={target.rate_coefficient} />}
                  <DetailRow label="降智检测耗时">
                    {latency(qualityResult?.quality.duration_ms ?? null)}
                  </DetailRow>
                  <DetailRow label="降智 HTTP 状态">
                    {qualityResult?.quality.http_status || "—"}
                  </DetailRow>
                  <DetailRow label="降智回复">
                    <div className="sub-check-reply">
                      {qualityResult?.quality.text || "—"}
                    </div>
                  </DetailRow>
                  {qualityResult?.quality.message && (
                    <DetailRow label="降智诊断">
                      <div className="sub-check-reply">
                        {qualityResult.quality.message}
                      </div>
                    </DetailRow>
                  )}
                </>
              )}
              {check.message && (
                <DetailRow label="检测诊断">
                  <div className="sub-check-reply">{check.message}</div>
                </DetailRow>
              )}
              {target.message && (
                <DetailRow label="同步诊断">
                  <div className="sub-check-reply">{target.message}</div>
                </DetailRow>
              )}
            </tbody>
          </table>}
          {check.model === "gpt-6-astra" && (account.id || target.check_source_id) && <QualityHistory path={target.check_source_id ? `/v1/sources/${encodeURIComponent(target.check_source_id)}/channel-checks/history?provider=${encodeURIComponent(target.channel)}` : `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/groups/${target.group_id}/quality-history`} revision={`${qualityResult?.checked_at}:${target.history?.total}:${target.history?.successful}`} />}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function ManagementChannelDrawer({
  row,
  prices,
  close,
  onOpenChannel,
}: {
  row: ManagementRow;
  prices?: ModelPrice[];
  close: () => void;
  onOpenChannel: () => void;
}) {
  const { account, configured } = row;
  const configuredMembers = configured ? channelMembers(configured) : [];
  const loadGroup = !configured && !!(row.target.summary_model || row.target.list_summary);
  const complete = useQuery({
    queryKey: ["sub-management-models", account.id, row.target.group_id],
    enabled: loadGroup,
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: ({ signal }) => readCompleteSubTarget(account.id, row.target.group_id, signal),
  });
  const target = complete.data ? { ...complete.data, observed_rates: complete.data.observed_rates ?? row.target.observed_rates } : row.target;
  const checks = complete.data ? modelChecks(target) : row.checks;
  const selected = complete.data ? checks.find(check => check.model === row.selected?.model) : row.selected;
  const modelsPending = loadGroup && !complete.data;
  const { supportedModels } = useModelFingerprints();
  const current = selected || checks[0];
  const readConfigured = !!(configured && row.target.list_summary && current?.model);
  const configuredDetail = useQuery({
    queryKey: ["sub-drawer-check", row.id, configured?.members, current?.model, current?.result?.checked_at],
    enabled: readConfigured,
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: ({ signal }) => readChannelCheckDetails(account, row.target, configured, current!.model, signal),
  });
  const latest = readConfigured ? configuredDetail.data?.selected : modelsPending ? undefined : current;
  const quality = groupQualityResult(target);
  const modelCount = checks.length;
  const availableCount = checks.filter(
    (check) => check.result?.availability.status === "success",
  ).length;
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="detail-panel sub-management-drawer">
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭渠道详情"
          >
            <X size={20} />
          </Dialog.Close>
          <Dialog.Title>{target.name}</Dialog.Title>
          <Dialog.Description className="detail-description">
            <span>{account.name}</span>
            <span>·</span>
            <span>{target.channel || target.platform}</span>
            {target.group_id > 0 && <span>· 分组 #{target.group_id}</span>}
          </Dialog.Description>
          <FingerprintPanel onlySupported targets={fingerprintTargets(row, selected ? [selected.model] : supportedModels)} revisions={configured ? Object.fromEntries(channelMembers(configured).map(m => [JSON.stringify([m.source_id,m.provider]),m.probe_fingerprint || ""])) : undefined}/>
          <div className="detail-channel-actions">
            <span className={`check-status ${target.state === "error" ? "fail" : "pass"}`}>
              {target.state === "error" ? <X size={15} /> : <Check size={15} />}
              {target.state === "error" ? "同步失败" : target.active ? "可检测" : "未激活"}
            </span>
            <div className="sub-management-channel-actions">
              {!account.id && configuredMembers.map(member => (
                <ChannelSettings
                  key={JSON.stringify([member.source_id, member.provider])}
                  triggerLabel={configuredMembers.length > 1 ? `编辑 ${member.source_name || member.source_id}` : "编辑渠道"}
                  row={{
                    provider: member.provider,
                    provider_name: member.name || member.provider,
                    source_id: member.source_id,
                    source_name: member.source_name,
                    model: current?.model || member.models[0] || "",
                  }}
                />
              ))}
              <button className="button small primary" onClick={onOpenChannel}>
                {configured ? "查看接入配置" : "添加到渠道"}
              </button>
            </div>
          </div>
          <section className="detail-section" aria-label="渠道概览">
            <h3>渠道概览</h3>
            <dl className="detail-stats sub-management-stats">
              <div>
                <dt>平台</dt>
                <dd>{target.platform || "—"}</dd>
              </div>
              <div>
                <dt>倍率</dt>
                <dd><BillingRate target={target} model={selected?.model} configured={configured} /></dd>
              </div>
              <div>
                <dt>可用模型</dt>
                <dd>{modelsPending ? "读取中…" : modelCount ? `${availableCount} / ${modelCount}` : "—"}</dd>
              </div>
              <div>
                <dt>API key</dt>
                <dd>{target.key_id ? `#${target.key_id}` : configured ? "配置密钥" : "尚未创建"}</dd>
              </div>
            </dl>
            {target.message && <p className="negative sub-management-message">{target.message}</p>}
          </section>
          <section className="detail-section detail-models" aria-label="模型检测结果">
            <h3>模型检测结果 <span className="count-badge">{modelCount}</span></h3>
            {modelsPending ? complete.isError
              ? <p role="alert">{complete.error.message}<button className="button small" onClick={() => void complete.refetch()}>重试</button></p>
              : <p role="status"><Spinner small/> 正在读取完整模型检测结果…</p>
            : checks.length ? (
              <ul className="detail-model-list">
                {checks.map((check) => {
                  const state = modelDetectionState(check);
                  const available = state === "done" && check.result?.availability.status === "success";
                  return (
                    <li key={check.model}>
                      {available ? <Check size={15} className="detail-model-check" /> : <CircleHelp size={15} />}
                      <div>
                        <strong>{check.model}</strong>
                        <small>{state === "skipped" || state === "rejected" ? check.message : check.result ? `最近检测 ${dateTime(check.result.checked_at)}` : "尚未检测"}</small>
                      </div>
                      <span className={available ? "detail-model-added" : "muted"}>
                        {importModelLabel(check)}
                      </span>
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="muted">暂无模型检测记录。</p>
            )}
          </section>
          <section className="detail-section detail-checks" aria-label="渠道能力">
            <h3>渠道能力</h3>
            <div className="sub-management-capabilities">
              <div>
                <span>降智检测</span>
                <strong><Verdict result={quality} history={target.history} /></strong>
              </div>
              <div>
                <span>远程压缩</span>
                <strong><CompactionStatus target={target} model={current?.model} /></strong>
              </div>
              <div>
                <span>Tool use</span>
                <strong><ToolUseStatus target={target} model={current?.model} /></strong>
              </div>
              <div>
                <span>模型匹配</span>
                <strong>{current ? <ModelMatch check={current} /> : "—"}</strong>
              </div>
            </div>
          </section>
          {current && (
            <section className="detail-section" aria-label="最近检测">
              <h3>最近检测 · {current.model}</h3>
              <dl className="detail-stats sub-management-stats">
                <div>
                  <dt>可用性</dt>
                  <dd><AvailabilityStatus check={current} /></dd>
                </div>
                <div>
                  <dt>首字延迟</dt>
                  <dd><ResponseLatency protocol={current.result?.availability.protocol} firstResponse={current.result?.availability.first_response_ms} created={current.result?.availability.response_created_ms} text={current.result?.availability.ttft_ms} /></dd>
                </div>
                <div>
                  <dt>单价核验</dt>
                  <dd>{latest ? <PriceStatus check={latest} prices={prices} /> :
                    configuredDetail.isError ? <button className="button small" onClick={() => void configuredDetail.refetch()}>重试读取单价</button> :
                    complete.isError ? "未能读取" : configuredDetail.isSuccess ? "无可确认记录" : "读取中…"}</dd>
                </div>
                <div>
                  <dt>最近检测</dt>
                  <dd>{current.result ? dateTime(current.result.checked_at) : "未检测"}</dd>
                </div>
              </dl>
              {(latest?.result?.availability.message || latest?.message) && (
                <p className="sub-management-message">{latest.result?.availability.message || latest.message}</p>
              )}
            </section>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

import { WorkbenchDisclosure, WorkbenchNotice, WorkbenchTabs } from "./Workbench";

export function Sub2apiChecks({ user = "account", initialPosition, onPositionChange, onRouting }: {
  user?: string;
  initialPosition?: ManagementPagePosition;
  onPositionChange?: (position: ManagementPagePosition) => void;
  onRouting?: () => void;
}) {
  const [section, setSection] = useState("channels");
  const client = useQueryClient();
  const detection = useChannelDetection(user);
  const [detectionProject, setDetectionProject] = useDetectionProject("management", user, detectionProjects);
  const sources = useConsoleSources(user);
  const [filters, setFilters] = useState(() => loadSubFilters(user));
  const [cursorState,setCursorState]=useState(()=>initialPosition?.owner===user ? initialPosition : {filter:"",cursor:""});
  const filterKey=JSON.stringify({...subFilterDefaults,...filters});
  const cursor=cursorState.filter===filterKey?cursorState.cursor:"";
  useEffect(()=>onPositionChange?.({owner:user,filter:filterKey,cursor}),[user,filterKey,cursor,onPositionChange]);
  const navigation = useRef<ManagementNavigation | undefined>(undefined);
  const [managementPreset, setManagementPreset] = useState<keyof typeof managementPresets>("operations");
  const [customColumns, setCustomColumns] = useTableColumns(managementColumnsStorageKey, managementColumnChoices);
  const activeColumns: readonly ManagementColumn[] = customColumns ?? managementPresets[managementPreset];
  const pageQuery=useManagementPage(user,filters,cursor,navigation,activeColumns.includes("price"),managementOverviewForColumns(filters.model,activeColumns));
  const navigatePage = (action: "previous" | "next", token?: string) => {
    if (!token) return;
    navigation.current = { owner: user, filter: JSON.stringify(filters), cursor: token, action, started: performance.now() };
    setCursorState({ filter: filterKey, cursor: token });
  };
  const pageData=pageQuery.data;
  const displayedPage=pageQuery.displayPage;
  const tablePending=!!displayedPage && !pageData;
  const tableModel=pageQuery.displayModel;
  // These controls are not the answer to the current row filter. Preserve
  // their mounted state while the next page is read. Previous results below
  // remain explicitly marked as updating and cannot authorize any action.
  const metadataRef=useRef<{owner:string;accounts:SubAccount[];rates:number[];platforms:string[];models:string[];detectionModels:string[];readAt:number}|undefined>(undefined);
  if(pageData)metadataRef.current={owner:user,accounts:pageData.accounts,rates:pageData.rates,platforms:pageData.platforms,models:pageData.models,detectionModels:pageData.detection_models,readAt:pageData.read_at_ms};
  const metadata=metadataRef.current?.owner===user?metadataRef.current:undefined;
  const accounts=metadata?.accounts || [];
  const [syncProgressSubmission, setSyncProgressSubmission] = useState<{ owner: string; generation: number; accounts: Map<string, number> }>(
    () => ({ owner: user, generation: 0, accounts: new Map() }),
  );
  const [accountActions, setAccountActions] = useState(new Set<string>());
  const accountActionsRef = useRef(new Set<string>());
  const actionKey = (id: string) => JSON.stringify([user, id]);
  const syncGeneration = syncProgressSubmission.owner === user ? syncProgressSubmission.generation : 0;
  const progressQuery=useSubSyncProgress(user,accounts,metadata?.readAt,syncGeneration);
  useSubSyncResultRefresh(user,pageData,progressQuery.data);
  const lastProgress = useRef<{owner: string; generation: number; accounts: SubAccount[]} | undefined>(undefined);
  if (progressQuery.data) lastProgress.current = { owner: user, generation: syncGeneration, accounts: progressQuery.data.data };
  const knownProgress = lastProgress.current?.owner === user ? lastProgress.current : undefined;
  const knownById = new Map(knownProgress?.accounts.map(account => [account.id, account]));
  const statusAccounts = progressQuery.data?.data || accounts.map(account => knownById.get(account.id) || account);
  const progressByAccount=new Map(statusAccounts.map(a=>[a.id,a]));
  const awaitingAccountProgress = (id: string) => syncGeneration > 0 &&
    (syncProgressSubmission.accounts.get(id) || 0) > (knownProgress?.generation ?? 0);
  const accountBusy = (id: string) => accountActions.has(actionKey(id)) || awaitingAccountProgress(id);
  function markProgress(ids: string[]) {
    setSyncProgressSubmission(previous => {
      const sameOwner = previous.owner === user;
      const generation = sameOwner ? previous.generation + 1 : 1;
      const progress = lastProgress.current;
      const confirmed = sameOwner && progress?.owner === user ? progress.generation : 0;
      const waiting = new Map(sameOwner ? [...previous.accounts].filter(([, version]) => version > confirmed) : []);
      ids.forEach(id => waiting.set(id, generation));
      return { owner: user, generation, accounts: waiting };
    });
  }
  function lockAccounts(ids: string[]) {
    const keys = ids.map(actionKey);
    if (keys.some(key => accountActionsRef.current.has(key)) || ids.some(awaitingAccountProgress)) return;
    keys.forEach(key => accountActionsRef.current.add(key));
    setAccountActions(new Set(accountActionsRef.current));
    return () => {
      keys.forEach(key => accountActionsRef.current.delete(key));
      setAccountActions(new Set(accountActionsRef.current));
    };
  }
  const query={...pageQuery,data:pageData?{data:accounts,cached:false}:undefined};
  const accountHeaders={data:metadata?{data:statusAccounts}:undefined,error:metadata?null:pageQuery.error};
  const prices={data:displayedPage?{data:displayedPage.context.prices}:undefined};
  const [form, setForm] = useState<{ account: SubAccount | null } | null>(null);
  const [settingsAccount, setSettingsAccount] = useState<SubAccount | null>(null);
  const setPage=(_page:number)=>setCursorState({filter:filterKey,cursor:""});
  const refreshPage=()=>{void progressQuery.refetch();void pageQuery.refetch();void detection.refetch();};
  const [modelSelectionRevision, setModelSelectionRevision] = useState(0);
  const {
    search,
    excludeName,
    accountId,
    model,
    maxRate,
    sort,
    availability,
    groupStatus,
    priceStatus,
    compaction,
    toolUse,
    quality,
    minQuality,
    platform,
    joined,
    modelMatch,
    fingerprint,
  } = filters;
  useEffect(() => saveSubFilters(user, filters), [user, filters]);
  const setSearch = (search: string) => setFilters((v) => ({ ...v, search }));
  const setAccountId = (accountId: string) =>
    setFilters((v) => ({ ...v, accountId }));
  const setModel = (model: string) => setFilters((v) => ({ ...v, model }));
  const setMaxRate = (maxRate: string) =>
    setFilters((v) => ({ ...v, maxRate }));
  const setSort = (sort: string) => setFilters((v) => ({ ...v, sort }));
  const setAvailability = (availability: string) =>
    setFilters((v) => ({ ...v, availability }));
  const setQuality = (quality: string) =>
    setFilters((v) => ({ ...v, quality }));
  const setPlatform = (platform: string) =>
    setFilters((v) => ({ ...v, platform }));
  const resultsUnavailable = !pageData;
  const resultsReadFailed = pageQuery.isError;
  const optimizationRoutes = displayedPage?.context.routes || {};
  const routeSources=Object.keys(optimizationRoutes);
  const routeQueries=routeSources.map(source=>({data:optimizationRoutes[source]}));
  function configuredCount(item:ManagedChannel){return managedRouteCount(item,routeSources,routeQueries);}
  // Keep the vocabulary selectable during a new query; no previous result
  // rows, counts or eligibility are rendered as the new filter's answer.
  const filterModels=[...new Set([...(metadata?.models || SUB_MODELS),...(model?[model]:[])])];
  const [configuredDialog, setConfiguredDialog] = useState<ManagedChannel | null>(null);
  const [managementDetail, setManagementDetail] = useState<ManagementRow | null>(null);
  const [filterTab, setFilterTab] = useState("conditions");
  const managementColumns = customColumns ?? managementPresets[managementPreset].filter(id => id !== "model" || !!tableModel);
  const columnView = customColumns ? "custom" : managementPreset;
  const [importing, setImporting] = useState<{
    account: SubAccount;
    target: SubTarget;
    configured?: ManagedChannel;
  } | null>(null);
  const dialogImports=useImportBindings(!!importing, importing ? {accountId:importing.account.id,groupId:importing.target.group_id} : undefined);
  const imports={data:displayedPage?{data:displayedPage.context.imports,labels:{},unavailable_sources:[]}:undefined};
  const [error, setError] = useState("");
  const [batchAccountAction, setBatchAccountAction] = useState<"detect" | "sync" | null>(null);
  const [removeError, setRemoveError] = useState("");
  const checksInFlight = useRef(new Set<string>());
  const [submittingChecks, setSubmittingChecks] = useState(new Set<string>());
  const [fingerprintsSubmitted, setFingerprintsSubmitted] = useState(0);
  const [syncSubmitted, setSyncSubmitted] = useState<{count:number;detect:boolean} | null>(null);
  const syncableAccounts = statusAccounts.filter(
    (account) => !pending(account.state),
  );
  const syncBatchBlocked = !!batchAccountAction || syncableAccounts.some(account => accountBusy(account.id));
  const [removing, setRemoving] = useState<SubAccount | null>(null);
  const rows=displayedPage?.rows || [], rates=metadata?.rates || [], platforms=metadata?.platforms || [];
  const total=displayedPage?.total || 0, eligibleCount=displayedPage?.eligible_count || 0;
  const effectiveMaxRate=maxRate;
  const availableModels=metadata?.detectionModels || SUB_MODELS;
  const extraModels=availableModels.filter(m=>!SUB_MODELS.includes(m));
  const modelSelectionKey = JSON.stringify(availableModels);
  const detectionModels = useMemo(() => loadSubModels(user, JSON.parse(modelSelectionKey)), [user, modelSelectionKey, modelSelectionRevision]);
  const modelSelectionNeedsReview = subModelSelectionNeedsReview(user);
  useEffect(() => initializeSubModels(user, JSON.parse(modelSelectionKey)), [user, modelSelectionKey]);
  // An explicit model filter is a direct probe request. Saved checkbox choices
  // only define the batch when no individual model is selected.
  const modelsToCheck = model ? [model] : detectionModels;
  const fingerprintModelsToCheck = modelsToCheck.filter(name =>
    pageData?.context.fingerprint_models.includes(name),
  );
  const allModelsSelected = detectionModels.length === availableModels.length;
  const currentPage=(displayedPage?.offset || 0)/25;
  const selectionID = (account: SubAccount, target: SubTarget, configured?: ManagedChannel) =>
    !account.id && configured ? `configured:${configured.provider}:${configured.engine}` : `${account.id}:${target.group_id}`;
  const [readingSelection,setReadingSelection]=useState(false);
  const selectionRead=useRef(false);
  const selectionRequest=useRef<AbortController|null>(null);
  const selectionScope=useRef("");
  const previewScope=useRef("");
  selectionScope.current=JSON.stringify([user,filterKey,cursor,pageData?.revision]);
  previewScope.current=JSON.stringify([user,filterKey,cursor]);
  useEffect(()=>{
    selectionRequest.current?.abort();
    return ()=>{selectionRequest.current?.abort();};
  },[user,filterKey,cursor]);
  async function completeSelection(preview=false){
    if(!pageData)throw Error("请先读取最新渠道结果");
    selectionRequest.current?.abort();
    const controller=new AbortController(),scope=preview?previewScope.current:selectionScope.current;
    selectionRequest.current=controller;
    try{
      const selected=preview
        ? await readManagementOptimizationPreview(user,filters,controller.signal)
        : await readManagementSelection(filters,pageData,controller.signal);
      if(scope!==(preview?previewScope.current:selectionScope.current))throw Error("筛选或渠道结果已变化，请重新选择操作");
      return selected;
    }finally{if(selectionRequest.current===controller)selectionRequest.current=null;}
  }
  async function mutate(
    key: string,
    path: string,
    body?: unknown,
    method = "POST",
  ) {
    const unlock = lockAccounts([key]);
    if (!unlock) return;
    const deleting = method === "DELETE";
    const report = deleting ? setRemoveError : setError;
    report("");
    try {
      await controlRequest(path, {
        method,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      markProgress([key]);
      if (deleting) setRemoving(null);
      void client.invalidateQueries({ queryKey: ["sub2api"] }, { cancelRefetch: false });
      void client.invalidateQueries({ queryKey: ["sub2api-headers"] }, { cancelRefetch: false });
    } catch (e) {
      report(e instanceof Error ? e.message : "操作失败");
    } finally {
      unlock();
    }
  }
  async function syncAccounts(detect = true, account?: SubAccount) {
    if ((account ? pending(account.state) : syncBatchBlocked || !syncableAccounts.length) || (detect && !detectionModels.length)) return;
    const ids = account ? [account.id] : syncableAccounts.map(item => item.id);
    const unlock = lockAccounts(ids);
    if (!unlock) return;
    if (!account) setBatchAccountAction(detect ? "detect" : "sync");
    setError("");
    setSyncSubmitted(null);
    try {
      const result = await controlRequest<{ queued: number | boolean }>(
        account ? `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/sync-selected` : detect ? "/v1/sub2api/accounts/sync-selected" : "/v1/sub2api/accounts/sync-only",
        { method: "POST", body: JSON.stringify(detect ? { models: detectionModels } : {}) },
      );
      setSyncSubmitted({count:account ? 1 : Number(result.queued),detect});
      markProgress(ids);
    } catch (e) {
      setError(e instanceof ApiError && e.status === 404 ? "同步检测服务待更新，请稍后重试。" : e instanceof Error ? e.message : "批量同步失败");
    } finally {
      unlock();
      if (!account) setBatchAccountAction(null);
    }
  }
  type CheckKind = "check" | "quality" | "compaction" | "tool-use";
  type BatchKind = CheckKind | "fingerprint";
  type BatchPreview = { kind: BatchKind; scope: string; total: number; items: {id:string; label:string; models:string[]}[]; targets: CheckSelection; fingerprintTargets: ReturnType<typeof fingerprintTargets> };
  const [batchPreview,setBatchPreview] = useState<BatchPreview|null>(null);
  const [batchReceipts,setBatchReceipts] = useState<{label:string;accepted:boolean;message:string}[]>([]);
  const submittingBatch = useRef(false);
  const [batchSubmitting,setBatchSubmitting] = useState(false);
  const batchScope = JSON.stringify([selectionScope.current, modelsToCheck, fingerprintModelsToCheck, [...detection.disabled].sort()]);
  const requestedModels = () => modelsToCheck;
  const checkKeys = (account: SubAccount, target: SubTarget, kind: CheckKind, configured?: ManagedChannel) =>
    (kind === "check" ? requestedModels() : kind === "quality" ? ["gpt-6-astra"] : [kind])
      .map(item => `${selectionID(account, target, configured)}:${item}`);
  const checkBusy = (account: SubAccount, target: SubTarget, kind: CheckKind, configured?: ManagedChannel) => {
    const current=progressByAccount.get(account.id)||account;
    if (account.id && accountBusy(account.id)) return true;
    if (pending(current.state) && current.job_kind === "sync") return true;
    if (checkKeys(account, target, kind, configured).some(key => submittingChecks.has(key))) return true;
    if (kind === "compaction") return pending(target.compaction_state || "");
    if (kind === "tool-use") return pending(target.tool_use_state || "");
    const models = kind === "quality" ? ["gpt-6-astra"] : requestedModels();
    return (target.models || modelChecks(target)).some(c => models.includes(c.model) && pending(c.state)) ||
      (!target.models && pending(target.state) && !pending(target.compaction_state || "") && !pending(target.tool_use_state || ""));
  };
  const canCheck = (account: SubAccount, target: SubTarget, kind: CheckKind, configured?: ManagedChannel) => {
    if (!pageData) return false;
    if ((kind === "compaction" || kind === "tool-use") && model && !isCapabilityCheckModel(model)) return false;
    const native = !account.id && configured;
    if (native ? !channelMembers(configured).length
      : !target.active || !target.key_id || target.state === "error") return false;
    return !(kind === "check" && !requestedModels().length) && !checkBusy(account, target, kind, configured);
  };
  type CheckSelection = { account: SubAccount; target: SubTarget; configured?: ManagedChannel }[];
  async function queueChecks(kind: CheckKind, selection: CheckSelection, manual = true) {
    const targets = selection.filter(row => (manual || detectionEnabled(row,detection.disabled)) && canCheck(row.account, row.target, kind, row.configured));
    if (!manual && targets.length !== selection.length) throw Error("部分检测对象已不可提交，请重新预览。");
    const keys = targets.flatMap(({account, target, configured}) => checkKeys(account, target, kind, configured));
    if (!targets.length || keys.some(key => checksInFlight.current.has(key))) return;
    keys.forEach(key => checksInFlight.current.add(key));
    setSubmittingChecks(new Set(checksInFlight.current));
    setError("");
    try {
      const site = targets.filter(t => !!t.account.id);
      const native = targets.filter(t => !t.account.id && t.configured).flatMap(({configured}) => {
        const members = channelMembers(configured!);
        // Probe every selected model once. Prefer an existing mapping; use the
        // first source for models not yet configured on any member.
        const models = kind === "check" ? requestedModels() : kind === "quality" ? ["gpt-6-astra"] : [];
        if (!models.length) return (kind === "tool-use" ? members : [members[0]]).map(m=>({source_id:m.source_id,provider:m.provider,models:[]}));
        const grouped = new Map<string, {source_id:string;provider:string;models:string[]}>();
        for (const model of models) {
          const member = members.find(m => m.models.includes(model)) || members[0];
          const key = `${member.source_id}:${member.provider}`;
          const target = grouped.get(key) || {source_id:member.source_id,provider:member.provider,models:[]};
          target.models.push(model);grouped.set(key,target);
        }
        return [...grouped.values()];
      });
      const requests: {label:string;request:Promise<{queued?:number}>}[] = [];
      if (site.length) requests.push({label:`站点渠道（${site.length} 个分组）`,request:controlRequest(`/v1/sub2api/${kind === "check" ? "checks" : `${kind}-checks`}`, {
        method:"POST", body:JSON.stringify({...(manual?{manual:true}:{}),targets:site.map(({account,target}) => ({account_id:account.id,group_id:target.group_id,...(kind === "check" ? {models:modelsToCheck} : {})}))}),
      })});
      if (native.length) requests.push({label:`原生渠道（${native.length} 个来源成员）`,request:controlRequest("/v1/channel-management/checks", {method:"POST",body:JSON.stringify({kind,...(manual?{manual:true}:{}),targets:native})})});
      const results = await Promise.allSettled(requests.map(item => item.request));
      const receipts = results.map((result,index) => ({
        label:requests[index].label,
        accepted:result.status === "fulfilled",
        message:result.status === "fulfilled"
          ? `已收到提交回执${typeof result.value?.queued === "number" ? `，排队 ${result.value.queued} 项` : ""}`
          : `未确认提交 · ${result.reason instanceof Error ? result.reason.message : "读取失败"}`,
      }));
      if (!manual) setBatchReceipts(receipts);
      await Promise.all([client.invalidateQueries({queryKey:["sub2api"]}),client.invalidateQueries({queryKey:["configured-checks"]})]);
      const failed = results.find(r => r.status === "rejected");
      if (manual && failed?.status === "rejected") throw failed.reason;
    } catch (e) {
      setError(e instanceof Error ? e.message : "检测提交失败");
    } finally {
      keys.forEach(key => checksInFlight.current.delete(key));
      setSubmittingChecks(new Set(checksInFlight.current));
    }
  }
  function batchFingerprintTargets(row: ManagementRow) {
    if (!detectionEnabled(row, detection.disabled)) return [];
    if (row.account.id) {
      const account = progressByAccount.get(row.account.id) || row.account;
      if (accountBusy(row.account.id) || pending(account.state) || !row.target.active || !row.target.key_id || row.target.state === "error") return [];
      return fingerprintTargets(row, fingerprintModelsToCheck);
    }
    if (!row.configured) return [];
    // For bulk sampling of gateway channels, do not probe models absent from
    // every member's configuration. The detail drawer still allows manual probes.
    const configuredModels = new Set(channelMembers(row.configured).flatMap(member => member.models));
    return fingerprintTargets(row, fingerprintModelsToCheck.filter(name => configuredModels.has(name)));
  }
  function preparedFingerprints(selection: Pick<ManagementSelection, "eligible" | "input">) {
    const { fingerprints, channels, imports, unavailable_sources, unavailable_imports } = selection.input;
    const index = sharedFingerprintIndex(fingerprints || [], channels, unavailable_sources || [], imports, unavailable_imports || []);
    const targets = new Map(selection.eligible.flatMap(batchFingerprintTargets).map(target => [fingerprintKey(target), target]));
    const ready = [...targets].filter(([key]) => !fingerprintBusy(index.get(key))).map(([, target]) => target);
    if (!ready.length) throw Error("当前筛选没有可发起指纹检测的渠道与模型，或检测已在排队。");
    if (ready.length > FINGERPRINT_BATCH_LIMIT) throw Error(`单次最多提交 ${FINGERPRINT_BATCH_LIMIT} 项指纹检测，请缩小渠道或模型范围。`);
    return ready;
  }
  async function batch(kind: BatchKind){
    if(selectionRead.current || !pageData || detection.isPending || detection.isError || detection.busy)return;
    selectionRead.current=true;setReadingSelection(true);setError("");setFingerprintsSubmitted(0);setBatchReceipts([]);
    try{
      const selected=await completeSelection();
      const targetSet = kind === "fingerprint" ? preparedFingerprints(selected) : [];
      const allowed = new Set(targetSet.map(fingerprintKey));
      const targets = kind === "fingerprint"
        ? selected.eligible.filter(row => batchFingerprintTargets(row).some(value => allowed.has(fingerprintKey(value))))
        : selected.eligible.filter(row=>detectionEnabled(row,detection.disabled)&&canCheck(row.account,row.target,kind,row.configured));
      if (!targets.length) throw Error("当前筛选没有可提交的检测对象。");
      const items = targets.map(row => ({
        id:row.id, label:`${row.account.name} / ${row.configured?.name || row.target.name}`,
        models:kind==="fingerprint" ? [...new Set(batchFingerprintTargets(row).filter(value=>allowed.has(fingerprintKey(value))).map(value=>value.model))]
          : kind==="check" ? modelsToCheck : kind==="quality" ? ["gpt-6-astra"] : [kind==="compaction" ? "压缩能力" : "Tool use 能力"],
      }));
      setBatchPreview({kind,scope:batchScope,total:selected.total,items,targets,fingerprintTargets:targetSet});
    }
    catch(e){setError(e instanceof Error?e.message:"完整筛选结果读取失败");}
    finally{selectionRead.current=false;setReadingSelection(false);}
  }
  async function confirmBatch() {
    const plan = batchPreview;
    if (!plan || submittingBatch.current) return;
    if (plan.scope !== batchScope || !pageData || detection.busy || detection.isError) {
      setBatchPreview(null); setError("筛选或检测状态已变化，请重新预览。"); return;
    }
    submittingBatch.current=true;setBatchSubmitting(true);
    try {
      // Reverify the full page-selection token just before the first write.
      const verified=await completeSelection();
      if (verified.revision !== pageData.revision) throw Error("渠道已变化，请重新预览。");
      if (plan.kind === "fingerprint") {
        const fresh = preparedFingerprints(verified).map(fingerprintKey).sort().join("\n");
        if (fresh !== plan.fingerprintTargets.map(fingerprintKey).sort().join("\n")) throw Error("指纹待检测范围已变化，请重新预览。");
        await controlRequest("/v1/model-fingerprints", {method:"POST",body:JSON.stringify({targets:plan.fingerprintTargets})});
        setFingerprintsSubmitted(plan.fingerprintTargets.length);
        void client.invalidateQueries({queryKey:["model-fingerprints"]});
      } else await queueChecks(plan.kind,plan.targets,false);
      setBatchPreview(null);
    } catch (error) {
      setBatchPreview(null);
      setError(error instanceof Error ? error.message : "检测提交未确认，请先刷新核对。");
    } finally {
      submittingBatch.current=false;setBatchSubmitting(false);
    }
  }
  const batchBusy=readingSelection || batchSubmitting;
  const batchDisabled=(kind:BatchKind)=>resultsUnavailable || !eligibleCount || detection.isPending || detection.isError || detection.busy || batchBusy || (
    kind === "fingerprint"
      ? !fingerprintModelsToCheck.length || (rows.length === total && !rows.some(row => batchFingerprintTargets(row).length))
      : (kind==="check"&&!modelsToCheck.length) || ((kind==="compaction"||kind==="tool-use")&&!!model&&!isCapabilityCheckModel(model)) || (rows.length===total && !rows.some(row=>detectionEnabled(row,detection.disabled)&&canCheck(row.account,row.target,kind,row.configured)))
  );
  const check=(selection?:CheckSelection)=>selection?queueChecks("check",selection):batch("check");
  const checkCompaction=(selection:CheckSelection)=>queueChecks("compaction",selection);
  const checkToolUse=(selection:CheckSelection)=>queueChecks("tool-use",selection);
  return (
    <div className="sub2api-page">
      <div className="data-heading management-page-heading">
        <div className="data-title"><h2>渠道管理</h2>
          <WorkbenchNotice urgent label="渠道管理操作状态" messages={[
            error,
            (detection.isError || detection.error) && (detection.error || "检测开关读取失败，已暂停批量检测。"),
          ]} />
          {resultsReadFailed && <button type="button" className="icon-button" aria-label="重试渠道结果读取" title="重试读取渠道结果" onClick={refreshPage}><RefreshCw size={15} /></button>}
          <WorkbenchNotice label="检测提交回执" messages={[
            ...batchReceipts.map(receipt => `${receipt.label}：${receipt.message}`),
            batchReceipts.some(receipt => !receipt.accepted) && "请先刷新并核对未确认的分支，已收到回执的分支不要重复提交。",
          ]} />
        </div>
        {onRouting && <button type="button" className="button small" onClick={onRouting}>路由顺序</button>}
      </div>
      <WorkbenchTabs label="管理视图" value={section} onChange={setSection} tabs={[["channels","渠道"],["sites","站点账号"]]} />
      <section className="data-panel sub-accounts management-sites" id="management-sites" aria-label="站点账号设置" hidden={section !== "sites"}>
        <div className="data-heading">
          <div className="data-title">
            <Globe2 size={19} />
            <h2>站点账号</h2>
            <span className="count-badge">{!query.data && !accountHeaders.data ? "…" : statusAccounts.length}</span>
            <WorkbenchNotice label="账号同步状态" messages={[
              syncSubmitted && (syncSubmitted.count
                ? `已提交 ${syncSubmitted.count} 个账号，${syncSubmitted.detect ? "正在后台同步并检测。" : "正在后台同步，不执行检测。"}`
                : "暂无可同步账号，已有任务会继续执行。"),
              modelSelectionNeedsReview && "额外模型暂未勾选。请检查检测模型设置后再运行批量检测。",
            ]} />
            <WorkbenchNotice urgent label="账号状态读取异常" messages={[
              progressQuery.isError && "实时进度更新失败，当前进度尚未确认。",
              accountHeaders.error && `账号状态读取失败：${accountHeaders.error.message}`,
            ]} />
          </div>
          <div className="sub-account-actions">
            {progressQuery.isError && <button type="button" className="icon-button" title="重试进度" aria-label="重试进度" onClick={()=>void progressQuery.refetch()}><RefreshCw size={15} /></button>}
            {accountHeaders.error && <button type="button" className="icon-button" title="重试读取账号" aria-label="重试读取账号" onClick={refreshPage}><RefreshCw size={15} /></button>}
            <button
              className="button small"
              aria-label="一键同步并检测"
              title={!detectionModels.length ? "请在检测模型设置中选择模型" : `仅检测已勾选的 ${detectionModels.length} 个模型。可用的目标模型同时检测模型指纹。${fingerprintNote}`}
              aria-busy={batchAccountAction === "detect"}
              disabled={
                !syncableAccounts.length ||
                !detectionModels.length ||
                syncBatchBlocked ||
                query.isPending ||
                query.isError
              }
              onClick={() => void syncAccounts()}
            >
              {batchAccountAction === "detect" ? (
                <Spinner small />
              ) : (
                <RefreshCw size={15} />
              )}
              {batchAccountAction === "detect"
                ? "正在提交…"
                : `一键同步并检测 · ${syncableAccounts.length} 个账号`}
            </button>
            <button className="button small" aria-label="一键同步" title="仅同步账号、分组和密钥信息，不执行模型或能力检测"
              aria-busy={batchAccountAction === "sync"}
              disabled={!syncableAccounts.length || syncBatchBlocked || query.isPending || query.isError}
              onClick={()=>void syncAccounts(false)}>
              {batchAccountAction === "sync" ? <Spinner small/> : <RefreshCw size={15}/>}
              {batchAccountAction === "sync" ? "正在提交…" : `一键同步 · ${syncableAccounts.length} 个账号`}
            </button>
            <button
              className="button primary small"
              onClick={() => setForm({ account: null })}
            >
              <Plus size={15} />
              添加账号
            </button>
          </div>
        </div>
        {settingsAccount && <PageBoundary onDismiss={() => setSettingsAccount(null)}><Suspense fallback={<RouteWorkspaceLoading title="站点账号设置" closeLabel="关闭站点设置" label="加载站点账号设置" close={() => setSettingsAccount(null)} context={settingsAccount.name} />}>
          <SiteAccountSettings account={settingsAccount} close={() => setSettingsAccount(null)} saved={() => {
          setSettingsAccount(null);
          void client.invalidateQueries({ predicate: q => ["management-query", "sub-sync-progress", "sub2api", "sub2api-headers", "sub2api-imports", "sub-channel-spend", "channel-spend", "metrics", "sub-check-detail", "sub-management-models"].includes(String(q.queryKey[0])) });
        }} /></Suspense></PageBoundary>}
        {form && (
          <AccountForm
            key={form.account?.id || "new"}
            initial={form.account}
            detectionModels={detectionModels}
            close={() => setForm(null)}
            saved={() => {
              setForm(null);
              void client.invalidateQueries({ queryKey: ["sub2api"] });
              void client.invalidateQueries({ queryKey: ["sub2api-headers"] });
              markProgress(form.account ? [form.account.id] : []);
            }}
          />
        )}
        {!metadata && resultsReadFailed ? null : !metadata ? (
          <TableLoading kind="accounts" rows={3} />
        ) : statusAccounts.length === 0 ? (
          <Empty title="暂无站点账号" icon={<Globe2 size={25} />} />
        ) : (
          <div className="sub-account-list" role="region" aria-label="站点账号列表" tabIndex={0}>
            <div className="sub-account-header" aria-hidden="true">
              <span>站点 / 网址</span>
              <span>账号</span>
              <span>余额</span>
              <span>同步状态</span>
              <span>操作</span>
            </div>
            <div className="sub-account-rows" role="list">
              {statusAccounts.map((a) => {
                const completed = a.completed_groups ?? a.targets.filter(
                    (t) => t.active && t.state === "done",
                  ).length,
                  total = a.active_groups ?? a.targets.filter((t) => t.active).length;
                const accountMessage = a.has_saved_password === false && /登录.*失效|会话.*失效/.test(a.message)
                  ? "站点会话已失效，且尚未保存登录密码；请补填一次密码，后续将自动登录。"
                  : a.message;
                const preparingKeys = pending(a.state) && (a.sync_phase === "preparing" || a.sync_phase === "waiting_keys" || a.sync_phase === "waiting_key_create");
                const keyProgress = a.progress?.keys_total ? `测试密钥 ${a.progress.keys_ready ?? 0}/${a.progress.keys_total}` : "";
                return (
                  <article className="sub-account" key={a.id} role="listitem" aria-label={a.name}>
                    <div className="sub-account-identity">
                      <strong title={a.name}>{a.name}</strong>
                      <SiteLink base={a.base}><span title={a.base}>{new URL(a.base).host}</span></SiteLink>
                    </div>
                    <div className="sub-account-email" title={a.login_name || a.email}>{a.login_name || a.email}</div>
                    <AccountBalance account={a} enabled={!!metadata} user={user} />
                    <div className="sub-account-status">
                      <span
                        className="sub-account-progress"
                        role={pending(a.state) ? "status" : undefined}
                      >
                        {pending(a.state) && <Spinner small />}
                        {pending(a.state) && a.sync_phase && syncPhaseLabel[a.sync_phase] || (a.sync_only && pending(a.state) ? (a.state === "queued" ? "等待同步" : "同步中") : stateLabel[a.state] || a.state)}
                        {total > 0 && ` · ${completed}/${total} 个分组`}
                      </span>
                      {accountMessage || a.progress || a.synced_at > 0 || pending(a.state) ? <Tip text={<div className="table-diagnostic-tip">
                        {a.sync_only && pending(a.state) && <div>仅同步信息，不执行检测</div>}
                        {pending(a.state) && keyProgress && <div>{keyProgress} 就绪{a.progress?.key_errors ? ` · ${a.progress.key_errors} 个不可用` : ""}</div>}
                        {pending(a.state) && !!a.sync_retry_at && <div>预计 {dateTime(a.sync_retry_at)} 重试</div>}
                        {pending(a.state) && a.probe_limits?.effective_rpm && <div>模型请求上限：{a.probe_limits.effective_rpm} RPM · {a.probe_limits.effective_concurrency} 并发（{a.probe_limits.source}）</div>}
                        {!a.sync_only && a.sync_models?.length && pending(a.state) && a.job_kind === "sync" ? <div>本次选择 {a.sync_models.length} 个模型；按分组协议能力检测</div> : null}
                        {a.progress && !a.sync_only && <div>模型进度：{a.progress.completed}/{a.progress.total} 完成 · {modelProgressDetails(a.progress)}</div>}
                        {!a.sync_only && a.progress?.running_models.length ? <div>正在检测：{[...new Set(a.progress.running_models)].join("、")}</div> : null}
                        {a.synced_at > 0 && <div>上次同步 {dateTime(a.synced_at)}</div>}
                        {accountMessage && <div>{accountMessage}</div>}
                      </div>}><small className="sub-account-message">{accountMessage && !pending(a.state) ? "查看状态"
                        : preparingKeys && keyProgress ? keyProgress
                        : a.progress && !a.sync_only ? `模型 ${a.progress.completed}/${a.progress.total}`
                        : a.synced_at > 0 ? dateTime(a.synced_at) : "查看进度"}</small></Tip> : null}
                    </div>
                    <div className="sub-account-actions">
                      <button className="button ghost small" aria-label={`设置站点 ${a.name}`} onClick={() => setSettingsAccount(a)}><Settings2 size={13} />设置</button>
                      {pending(a.state) ? (
                        <button
                          className="button small"
                          disabled={accountBusy(a.id)}
                          onClick={() =>
                            void mutate(a.id, `/v1/sub2api/accounts/${a.id}/stop`)
                          }
                        >
                          <Square size={13} />
                          停止
                        </button>
                      ) : (
                        <>
                          <button
                            className="button small"
                            aria-label="同步并检测"
                            title={!detectionModels.length ? "请在检测模型设置中选择模型" : `仅检测已勾选的 ${detectionModels.length} 个模型`}
                            disabled={accountBusy(a.id) || !detectionModels.length}
                            aria-busy={accountBusy(a.id)}
                            onClick={() => void syncAccounts(true, a)}
                          >
                            {accountBusy(a.id) ? <Spinner small /> : <RefreshCw size={13} />}
                            同步并检测
                          </button>
                          <button
                            className="button ghost small"
                            disabled={accountBusy(a.id)}
                            onClick={() => setForm({ account: a })}
                          >
                            重新登录
                          </button>
                          <button
                            className="icon-button"
                            aria-label={`移除账号 ${a.name}`}
                            disabled={accountBusy(a.id)}
                            onClick={() => { setRemoveError(""); setRemoving(a); }}
                          >
                            <Trash2 size={15} />
                          </button>
                        </>
                      )}
                    </div>
                  </article>
                );
              })}
            </div>
          </div>
        )}
      </section>
      {removing && <RemovalDialog title="移除站点账号" context={removing.name} busy={accountBusy(removing.id)} error={removeError}
        close={() => { setRemoving(null); setRemoveError(""); }}
        confirm={() => void mutate(removing.id, `/v1/sub2api/accounts/${encodeURIComponent(removing.id)}`, undefined, "DELETE")}>
        将删除本地凭据和检测记录。上游测试 Key 不会被撤销。
      </RemovalDialog>}
      {batchPreview && <Dialog.Root open onOpenChange={open=>{if(!open&&!submittingBatch.current)setBatchPreview(null);}}>
        <Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="guide-dialog compact-dialog"
          onEscapeKeyDown={event => { if (submittingBatch.current) event.preventDefault(); }}
          onPointerDownOutside={event => { if (submittingBatch.current) event.preventDefault(); }}>
          <CompactDialogHeader context="当前筛选的全部页" busy={batchSubmitting}>核对批量检测范围</CompactDialogHeader>
          <div className="compact-dialog-scroll">
            <p className="dialog-summary">
              <span>本次计划 {batchPreview.items.length} / {batchPreview.total} 个渠道；跳过 {Math.max(0,batchPreview.total-batchPreview.items.length)} 个</span>
              <span>{batchPreview.kind==="fingerprint" ? `去重后 ${batchPreview.fingerprintTargets.length} 项` : `${batchPreview.items.reduce((count,item)=>count+item.models.length,0)} 个检测项`}</span>
            </p>
            <details><summary>渠道与模型</summary><ul className="dialog-preview-list">
              {batchPreview.items.map(item=><li key={item.id}><strong>{item.label}</strong><small>{item.models.join("、")}</small></li>)}
            </ul></details>
            <p className="dialog-inline-note">每项可能发送多次请求，产生上游费用。</p>
            {batchPreview.scope!==batchScope && <p className="dialog-inline-note" role="alert">筛选或检测数据已变化，请重新预览。</p>}
          </div>
          <div className="compact-dialog-footer">
            <Dialog.Close className="button" disabled={batchSubmitting}>取消检测</Dialog.Close>
            <button className="button primary" disabled={batchSubmitting || batchPreview.scope!==batchScope} onClick={()=>void confirmBatch()}>{batchSubmitting ? "正在核对并提交…" : "确认提交检测"}</button>
          </div>
        </Dialog.Content></Dialog.Portal>
      </Dialog.Root>}
      <section className="data-panel" id="management-channels" hidden={section !== "channels"} aria-labelledby="management-channel-title">
        <div className="sr-only"><h3 id="management-channel-title">渠道列表</h3>{resultsUnavailable ? "—" : total}</div>
        <div className="management-toolbar">
          <div className="management-query">
            <label className="search-field">
              <Search size={16} />
              <input
                aria-label="搜索 sub2api 分组"
                placeholder="搜索站点、分组、渠道…"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(0);
                }}
              />
            </label>
            <WorkbenchDisclosure className="management-filters" label="筛选">
            <WorkbenchTabs label="渠道管理筛选面板" value={filterTab} onChange={setFilterTab} tabs={[["conditions","条件"],["columns","数据列"]]} />
            <section hidden={filterTab !== "conditions"}>
            <div className="filters sub-filters" role="region" aria-label="渠道管理筛选" tabIndex={0}>
              <label className="select-field">
                <select
                  aria-label="分组状态筛选"
                  title="按站点分组是否可用筛选，与模型检测结果无关"
                  value={groupStatus}
                  onChange={(e) => {
                    setFilters((v) => ({ ...v, groupStatus: e.target.value }));
                    setPage(0);
                  }}
                >
                  <option value="available">可用分组</option>
                  <option value="all">全部分组</option>
                  <option value="unavailable">不可用分组</option>
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select aria-label="是否已加入渠道筛选" value={joined} onChange={e=>{setFilters(v=>({...v,joined:e.target.value}));setPage(0);}} title="依据 API key 的实际路由判断；读取不完整的渠道不会误判为未加入">
                  <option value="">全部接入状态</option>
                  <option value="joined">已加入 API key</option>
                  <option value="unjoined">未加入任何 API key</option>
                </select><ChevronDown size={13}/>
              </label>
              <label className="search-field sub-name-exclude">
                <input
                  aria-label="排除渠道名"
                  placeholder="排除渠道名，如 kiro"
                  title="排除名称包含此字符串的渠道，不区分大小写"
                  maxLength={512}
                  value={excludeName}
                  onChange={(e) => {
                    setFilters((v) => ({ ...v, excludeName: e.target.value }));
                    setPage(0);
                  }}
                />
              </label>
              <label className="select-field">
                <Globe2 size={15} />
                <select
                  aria-label="sub2api 账号筛选"
                  title={accountId === UNASSIGNED_ACCOUNT ? "未归属渠道" : accounts.find(a => a.id === accountId)?.name || "全部账号"}
                  value={accountId}
                  onChange={(e) => {
                    setAccountId(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">全部账号</option>
                  <option value={UNASSIGNED_ACCOUNT}>未归属渠道</option>
                  {accountId && accountId !== UNASSIGNED_ACCOUNT && !accounts.some((a) => a.id === accountId) && (
                    <option value={accountId}>已选账号（当前不可用）</option>
                  )}
                  {accounts.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name} · {a.email}
                    </option>
                  ))}
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="检测模型筛选"
                  title={model || "全部模型"}
                  value={model}
                  onChange={(e) => {
                    setModel(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">全部模型</option>
                  {filterModels.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="平台筛选"
                  value={platform}
                  onChange={(e) => {
                    setPlatform(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">全部平台</option>
                  {platform && !platforms.includes(platform) && (
                    <option value={platform} disabled>
                      {platform}（当前无匹配）
                    </option>
                  )}
                  {platforms.map((p) => (
                    <option key={p} value={p}>
                      {p}
                    </option>
                  ))}
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="倍率上限筛选"
                  value={effectiveMaxRate}
                  onChange={(e) => {
                    setMaxRate(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">全部倍率</option>
                  <option value="mismatch">倍率不一致</option>
                  {rates.map((rate) => (
                    <option key={rate} value={rate}>
                      ≤ {rate}
                    </option>
                  ))}
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="倍率排序"
                  value={sort}
                  onChange={(e) => {
                    setSort(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="asc">倍率 ↑</option>
                  <option value="desc">倍率 ↓</option>
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="可用性筛选"
                  value={availability}
                  onChange={(e) => {
                    setAvailability(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">全部可用性</option>
                  <option value="success">
                    {model ? "可用" : "至少一个模型可用"}
                  </option>
                  <option value="error">
                    {model ? "检测失败" : "有模型检测失败"}
                  </option>
                  <option value="untested">
                    {model ? "未检测" : "有模型未检测"}
                  </option>
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field sub-price-filter">
                <select
                  aria-label="单价是否异常筛选"
                  title={model ? "按所选模型的倍率前单价与价格设置比较；参考价未确认时也可按数值是否匹配筛选" : "任一模型异常则为异常；全部模型确认正常才为正常。未确认匹配／不匹配：包含至少一个符合条件的未确认模型；缺少单价或定价不算匹配"}
                  value={priceStatus}
                  onChange={(e) => {
                    setFilters((v) => ({ ...v, priceStatus: e.target.value }));
                    setPage(0);
                  }}
                >
                  <option value="">全部单价</option>
                  <option value="abnormal">单价异常</option>
                  <option value="normal">单价正常</option>
                  <option value="unconfirmed">单价未确认</option>
                  <option value="unconfirmed_match">未确认 · 定价匹配</option>
                  <option value="unconfirmed_mismatch">未确认 · 定价不匹配</option>
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select aria-label="模型匹配筛选" value={modelMatch} onChange={e=>{setFilters(v=>({...v,modelMatch:e.target.value}));setPage(0);}}>
                  <option value="">全部模型匹配</option>
                  {Object.entries(modelMatchLabels).map(([value,label])=><option key={value} value={value}>{label}</option>)}
                </select><ChevronDown size={13}/>
              </label>
              <label className="select-field">
                <select aria-label="模型指纹筛选" disabled={!pageData} value={fingerprint} onChange={e => {setFilters(v => ({...v,fingerprint:e.target.value}));setPage(0);}}>
                  <option value="">全部模型指纹</option>
                  {Object.entries(fingerprintLabels).map(([value,label])=><option key={value} value={value}>{label}</option>)}
                </select><ChevronDown size={13}/>
              </label>
              <label className="select-field">
                <select aria-label="是否支持压缩筛选" value={compaction} onChange={e => {setFilters(v => ({...v, compaction: e.target.value})); setPage(0);}}>
                  <option value="">全部压缩能力</option>
                  {Object.entries(compactionLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                </select><ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select aria-label="Tool use 是否支持筛选" value={toolUse} onChange={e => {setFilters(v => ({...v, toolUse: e.target.value})); setPage(0);}}>
                  <option value="">Tool use 能力</option>
                  {Object.entries(toolUseLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                </select><ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="降智筛选"
                  value={quality}
                  onChange={(e) => {
                    setQuality(e.target.value);
                    setPage(0);
                  }}
                >
                  <option value="">Astra 降智</option>
                  <option value="pass">不降智</option>
                  <option value="fail">降智</option>
                  <option value="inconclusive">无法判定</option>
                  <option value="error">检测失败</option>
                </select>
                <ChevronDown size={13} />
              </label>
              <label className="select-field">
                <select
                  aria-label="不降智概率筛选"
                  value={minQuality}
                  onChange={(e) => {
                    setFilters((v) => ({ ...v, minQuality: e.target.value }));
                    setPage(0);
                  }}
                >
                  <option value="">全部不降智概率</option>
                  {Array.from({ length: 11 }, (_, i) => i * 10).map((percent) => (
                    <option key={percent} value={percent}>不降智 ≥ {percent}%</option>
                  ))}
                </select>
                <ChevronDown size={13} />
              </label>
              <button
                className="filter-chip sub-filter-reset"
                onClick={() => {
                  const defaults = JSON.stringify(subFilterDefaults);
                  if (defaults !== JSON.stringify(filters) || cursor)
                    navigation.current = { owner: user, filter: defaults, cursor: "", action: "reset", started: performance.now() };
                  setFilters({ ...subFilterDefaults });
                  setPage(0);
                }}
              >
                <X size={12} /> 重置筛选
              </button>
            </div>
            </section>
            <section hidden={filterTab !== "columns"} aria-label="渠道管理数据列">
              <label className="channel-filter-field">列预设<span className="select-field">
                <select aria-label="表格视图" value={columnView} onChange={event => {
                  setManagementPreset(event.target.value as keyof typeof managementPresets);
                  setCustomColumns(null);
                }}>
                  <option value="operations">运行</option>
                  <option value="checks">检测</option>
                  <option value="finance">费用</option>
                  <option value="all">全部</option>
                  {customColumns && <option value="custom" disabled>自定义</option>}
                </select>
                <ChevronDown size={13} aria-hidden="true" />
              </span></label>
              <TableColumns inline choices={managementColumnChoices} value={managementColumns} onChange={setCustomColumns}
                onReset={() => {setManagementPreset("operations");setCustomColumns(null);}} />
            </section>
            </WorkbenchDisclosure>
          </div>
          <div className="management-actions" role="group" aria-label="渠道管理操作">
            <DetectionAction projects={detectionProjects} project={detectionProject} onSelect={project => { setDetectionProject(project); setFingerprintsSubmitted(0); }} selectDisabled={batchBusy}>
              <button
                className="button small"
                aria-label={`${detectionProjectLabels[detectionProject]} · ${eligibleCount} 个渠道`}
                aria-busy={batchBusy}
                disabled={batchDisabled(detectionProject)}
                title={detectionProject === "fingerprint" ? (fingerprintModelsToCheck.length ? `对当前筛选范围提交指纹检测 · ${fingerprintModelsToCheck.length} 个适用模型` : "请选择支持指纹检测的模型") : detectionProject === "compaction" ? capabilityCheckScope : detectionProject === "tool-use" ? toolUseDescription : undefined}
                onClick={() => void batch(detectionProject)}
              >
                {batchBusy ? (
                  <Spinner small />
                ) : (
                  <ScanLine size={15} />
                )}
                {detectionProjectLabels[detectionProject]}
              </button>
            </DetectionAction>
            <button
              className="button small sub-check-icon"
              aria-label="刷新渠道管理"
              title="刷新数据"
              onClick={refreshPage}
              disabled={query.isFetching}
            >
              <RefreshCw size={15} className={query.isFetching || pageQuery.updating ? "spin" : ""} />
            </button>
            <div className="sub-model-check-group" role="group" aria-label="模型检测与设置">
              <button
                className="button primary small"
                aria-label={`${model ? "检测所选模型" : allModelsSelected ? "检测全部模型" : `检测已选 ${detectionModels.length} 个模型`} · ${eligibleCount} 个渠道`}
                disabled={batchDisabled("check")}
                title={!modelsToCheck.length ? "请在检测模型设置中选择模型" : `检测 ${eligibleCount} 个渠道，${modelsToCheck.length} 个模型；可用的 Astra / 6.1-sol 同时检测模型指纹，每模型 3 次请求`}
                onClick={() => void check()}
              >
                <ScanLine size={15} />
                {model ? "检测所选模型" : allModelsSelected ? "检测全部模型" : "检测已选模型"}
              </button>
              <SubModelSettings models={detectionModels} extraModels={extraModels} onSave={(models) => {
                saveSubModels(user, models, availableModels);
                setModelSelectionRevision(v => v + 1);
              }} />
            </div>
            <CreateChannel sources={sources.data?.data || []} />
            <ChannelOptimization
              disabled={!pageData || !total || readingSelection}
              snapshot={async()=>managementOptimizationSnapshot(await completeSelection(true),filters)}
            />
            <WorkbenchNotice label="渠道管理状态" messages={[
              !!fingerprintsSubmitted && `已提交 ${fingerprintsSubmitted} 项指纹检测`,
              pageQuery.updating && "正在更新渠道结果，暂显示更新前内容…",
              resultsReadFailed && `暂时无法读取渠道：${pageQuery.error?.message || "请重试"}`,
            ]} />
          </div>
        </div>
        {!displayedPage && resultsReadFailed ? (
          <Empty title="暂时无法读取渠道">检测结果尚未完整读取，暂时无法判断是否有匹配渠道。请重试读取。</Empty>
        ) : !displayedPage ? (
          <TableLoading kind="checks" rows={6} />
        ) : rows.length === 0 ? (
          <Empty title={tablePending ? (resultsReadFailed ? "结果尚未确认" : "正在读取当前渠道结果") : "没有匹配的渠道"}>
            添加渠道、同步站点账号或调整筛选后查看。
          </Empty>
        ) : (
          <>
            <div className="table-scroll sub-results-table" aria-busy={tablePending} aria-hidden={resultsReadFailed || undefined} inert={tablePending} style={resultsReadFailed?{visibility:"hidden"}:undefined}>
              <table className="channel-table check-table sub-table" data-columns={columnView}>
                <thead>
                  <tr>
                    <th scope="col">渠道 / 站点</th>
                    {managementColumns.map(id => <th scope="col" key={id} data-column={id}>
                      {managementColumnHelp[id] ? <Tip text={managementColumnHelp[id]}>{managementColumnLabels[id]} <CircleHelp size={12} aria-hidden="true" /></Tip> : managementColumnLabels[id]}
                    </th>)}
                    <th scope="col">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows
                    .map((managementRow) => {
                      const { id, account, target: t, checks, selected, configured } = managementRow;
                      const currentAccount=progressByAccount.get(account.id)||account;
                      const scopedModels = pending(currentAccount.state) && currentAccount.job_kind === "sync" ? currentAccount.sync_models : undefined;
                      const checkByModel = new Map(checks.map(c=>[c.model,c]));
                      const overviewChecks = scopedModels?.length ? [...new Set(scopedModels)].map(model => checkByModel.get(model) || {model,state:"idle",message:"",result:null}) : checks;
                      const scopeNote = scopedModels?.length && checks.length ? `本次已选 ${overviewChecks.length} 个模型 · 全部 ${checks.length} 个模型记录可在详情查看` : undefined;
                      const liveGroup = progressByAccount.get(account.id)?.progress?.groups.find(g=>g.group_id===t.group_id);
                      const openDrawer = () => { if (pageData) setManagementDetail(managementRow); };
                      return (
                      <tr
                        key={id}
                        className="sub-management-row"
                        tabIndex={tablePending ? -1 : 0}
                        onClick={(event) => {
                          const element = event.target as HTMLElement;
                          if (element.closest("button, a, input, select")) return;
                          openDrawer();
                        }}
                        onKeyDown={(event) => {
                          if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) {
                            event.preventDefault();
                            openDrawer();
                          }
                        }}
                        aria-label={`查看 ${account.name} ${t.name} 渠道详情`}
                      >
                        <td>
                          <strong title={t.name}>{t.name}</strong>
                          <small className="check-source" title={[account.name, t.channel].filter(Boolean).join(" · ")}>
                            {account.name}
                            {t.channel ? ` · ${t.channel}` : ""}
                          </small>
                        </td>
                        {managementColumns.map(id => {
                          const content = () => {
                            switch (id) {
                              case "model": return <span className="mono management-table-label" title={tableModel}>{tableModel || ((t.list_overview?.total ?? checks.length) ? `${t.list_overview?.total ?? checks.length} 个模型` : "—")}</span>;
                              case "platform": return <>
                                <span title={t.platform}>{t.platform}</span>
                                <small className="check-source">{t.key_id ? `key #${t.key_id}` : configured ? "配置密钥" : "尚未创建"}</small>
                              </>;
                              case "protocol": return <ChannelProtocolSummary row={managementRow} imports={imports.data} routes={optimizationRoutes}/>;
                              case "rate": return <BillingRate target={t} model={tableModel} configured={configured} compact />;
                              case "availability": return t.state === "error" ? (
                            <span className="check-status error">同步失败</span>
                          ) : selected ? (
                            <AvailabilityStatus check={selected} />
                          ) : (
                            liveGroup && scopedModels?.length ?
                              <Tip text={modelProgressDetails(liveGroup)}>
                                <span className="check-status">{liveGroup.completed}/{liveGroup.total} 完成</span>
                              </Tip> : <GroupAvailability checks={overviewChecks} scope={scopeNote} overview={scopeNote ? undefined : t.list_overview} />
                          );
                              case "latency": return <ResponseLatency
                            protocol={selected?.result?.availability.protocol}
                            firstResponse={selected?.result?.availability.first_response_ms}
                            created={
                              selected?.result?.availability.response_created_ms
                            }
                            text={selected?.result?.availability.ttft_ms}
                          />;
                              case "match": return selected ? (
                            <ModelMatch check={selected} />
                          ) : (
                            liveGroup && scopedModels?.length ?
                              <Tip text={`已完成 ${liveGroup.completed}/${liveGroup.total} · 刷新渠道查看匹配结果`}><span className="muted">待更新</span></Tip>
                              : <GroupModelMatch checks={overviewChecks} scope={scopeNote} />
                          );
                              case "price": return t.list_summary ? <span className="muted">读取中…</span> : selected ? <PriceStatus check={selected} prices={prices.data?.data} /> : <GroupPriceStatus checks={checks} prices={prices.data?.data} />;
                              case "fingerprint": return <FingerprintCell entries={displayedPage?.context.fingerprints[managementRow.id] || []} supportedModels={displayedPage?.context.fingerprint_models} channel={[account.name, t.name].filter(Boolean).join(" / ")}/>;
                              case "quality": {
                                const result = groupQualityResult(t);
                                return <span className="management-metric"><Verdict result={result} history={t.history} /><QualityProbability history={t.history} checkedAt={result?.checked_at} /></span>;
                              }
                              case "compaction": return <span className="management-metric"><CompactionStatus target={t} model={tableModel || undefined} /><button className="icon-button" disabled={!canCheck(account, t, "compaction", configured)} aria-busy={checkBusy(account, t, "compaction", configured)}
                                title="重新检测远程压缩" aria-label={`检测 ${account.name} ${t.name} 的远程压缩`} onClick={() => void checkCompaction([{account, target:t, configured}])}>{checkBusy(account, t, "compaction", configured) ? <Spinner small /> : <RefreshCw size={13} />}</button></span>;
                              case "tool-use": return <span className="management-metric"><ToolUseStatus target={t} model={tableModel || undefined} /><button className="icon-button" disabled={!canCheck(account, t, "tool-use", configured)} aria-busy={checkBusy(account, t, "tool-use", configured)}
                                title="重新检测 Tool use" aria-label={`检测 ${account.name} ${t.name} 的 Tool use`} onClick={() => void checkToolUse([{account, target:t, configured}])}>{checkBusy(account, t, "tool-use", configured) ? <Spinner small /> : <RefreshCw size={13} />}</button></span>;
                              case "diagnostics": return <CheckDetails
                            key={tableModel || "all"}
                            account={account}
                            target={t}
                            checks={checks}
                            selected={selected}
                            prices={prices.data?.data}
                            configured={configured}
                          />;
                              case "checked": {
                                const at = selected ? selected.result?.checked_at || 0 : t.list_overview?.checked_at ?? Math.max(0, ...checks.map(c => c.result?.checked_at || 0));
                                return <span className="mono" title={selected && pending(selected.state) && selected.result ? "上次检测结果" : undefined}>{at ? dateTime(at) : "—"}</span>;
                              }
                              case "batch": return <button type="button" role="switch" aria-label={`${account.name} ${t.name} 的批量检测开关`}
                            aria-checked={detectionEnabled(managementRow,detection.disabled)} className="channel-detection-switch"
                            disabled={tablePending || detection.isPending || detection.isError || detection.isSaving(managementRow)}
                            title="关闭后跳过批量检测，仍可点击此渠道的检测按钮。已提交的任务继续执行。"
                            onKeyDown={e=>e.stopPropagation()}
                            onClick={()=>void detection.save(managementRow,!detectionEnabled(managementRow,detection.disabled))}><span/></button>;
                            }
                          };
                          return <td key={id} data-column={id}>{content()}</td>;
                        })}
                        <td>
                          <div className="sub-row-actions">
                          <button
                            className="button small"
                            aria-label={`检测 ${account.name} ${t.name}`}
                            title={checkBusy(account, t, "check", configured) ? "检测正在排队或执行，请稍候" : model ? `检测 ${model}；可用的 Astra / 6.1-sol 同时检测模型指纹，每模型 3 次请求` : `检测已勾选的 ${modelsToCheck.length} 个模型，包含可用的 Astra / 6.1-sol 模型指纹`}
                            disabled={!canCheck(account, t, "check", configured)}
                            aria-busy={checkBusy(account, t, "check", configured)}
                            onClick={() =>
                              void check([
                                { account, target: t, configured },
                              ])
                            }
                          >
                            {checkBusy(account, t, "check", configured) ? <Spinner small /> : <ScanLine size={13} />}
                            {model ? "检测此模型" : allModelsSelected ? "检测全部模型" : `检测已选 ${detectionModels.length} 个模型`}
                          </button>
                          <button
                            className="button small"
                            disabled={tablePending}
                            onClick={() =>
                              configured ? setConfiguredDialog(configured) : setImporting({
                                account,
                                target: t,
                                configured,
                              })
                            }
                          >
                            <Plus size={13} />
                            {(() => {
                              if (configured) {
                                const counts=configuredCount(configured);
                                if (!counts) return "查看接入状态";
                                return counts.count ? `已添加 · ${counts.partial ? "至少 " : ""}${counts.count} 个 key` : counts.partial ? "查看接入状态" : "添加到渠道";
                              }
                              if (imports.data?.data.some(i => i.kind === "configured" && boundGroups(i).some(g => g.account_id === account.id && g.group_id === t.group_id))) return "已配置 · 查看路由";
                              const count =
                                imports.data?.data.filter(
                                  (i) =>
                                    i.kind !== "configured" && i.account_id === account.id && i.group_id === t.group_id,
                                ).length || 0;
                              return count
                                ? `已添加 · ${count} 个 key`
                                : "添加到渠道";
                            })()}
                          </button>
                          </div>
                        </td>
                      </tr>
                      );
                    })}
                </tbody>
              </table>
            </div>
            <div className="table-footer" aria-busy={tablePending}>
              <span>
                {tablePending ? "正在核对当前结果" : <>共 {total} 个渠道 · {eligibleCount} 个可发起检测</>}
              </span>
              <div className="sub-pagination">
                <button
                  className="button small"
                  disabled={!pageData?.previous_cursor}
                  onClick={() => navigatePage("previous", pageData?.previous_cursor)}
                >
                  上一页
                </button>
                <span>
                  {currentPage + 1} / {Math.max(1, Math.ceil(total / 25))}
                </span>
                <button
                  className="button small"
                  disabled={!pageData?.next_cursor}
                  onClick={() => navigatePage("next", pageData?.next_cursor)}
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </section>
      {importing && (
        <PageBoundary onDismiss={() => setImporting(null)}><Suspense fallback={<RouteWorkspaceLoading label="加载渠道设置" close={() => setImporting(null)} context={importing.target.name} />}>
        <Sub2apiImport
          sources={sources}
          imports={dialogImports}
          account={importing.account}
          configured={importing.configured}
          target={accounts.find(a => a.id === importing.account.id)?.targets.find(t => t.group_id === importing.target.group_id) || importing.target}
          prices={prices.data?.data}
          close={() => setImporting(null)}
        />
        </Suspense></PageBoundary>
      )}
      {configuredDialog && <ConfiguredChannelDialog item={configuredDialog} prices={prices.data?.data} close={() => setConfiguredDialog(null)} />}
      {managementDetail && (
        <ManagementChannelDrawer
          row={managementDetail}
          prices={prices.data?.data}
          close={() => setManagementDetail(null)}
          onOpenChannel={() => {
            const ready = managementDetail;
            setManagementDetail(null);
            if (ready.configured) {
              setConfiguredDialog(ready.configured);
            } else {
              setImporting({
                account: ready.account,
                target: ready.target,
                configured: ready.configured,
              });
            }
          }}
        />
      )}
    </div>
  );
}
