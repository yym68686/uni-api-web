import { useSubSyncProgress } from "./subSyncProgress";
import type { SyncProgress } from "./subSyncProgress";
import { fingerprintTargets, fingerprintLabels, FingerprintCell, FingerprintPanel, fingerprintNote } from "./ModelFingerprints";
import { isCapabilityCheckModel, capabilityCheckScope } from "./sub2apiModels";
import { CopyProbeCurl } from "./CopyProbeCurl";
import { TableLoading, DetailLoading } from "./PageLoading";
import { useEffect, useMemo, useRef, useState } from "react";
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
  Square,
  Trash2,
  X,
} from "lucide-react";
import { ApiError, controlRequest } from "./api";
import { accountBalanceQuery } from "./accountBalance";
import type { AccountBalance as SubAccountBalance } from "./accountBalance";
import { BalanceAmount } from "./BalanceAmount";
import { AccountForm } from "./SiteAccountForm";
import { ResponseLatency } from "./LatencyBadge";
import { Empty, Spinner, Tip } from "./ui";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
import { useImportBindings, boundGroups } from "./sub2apiImports";
import { Sub2apiImport } from "./Sub2apiImport";
import { readCompleteSubTarget } from "./subTargetDetails";
import { CreateChannel } from "./CreateChannel";
import { ChannelOptimization } from "./ChannelOptimization";
import { ChannelProtocolSummary } from "./ChannelProtocolSummary";
import { useManagementPage, readManagementSelection, managementOptimizationSnapshot } from "./managementQuery";
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
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { time } from "./format";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import type { QualitySummary } from "./QualityHistory";
import { SiteLink } from "./ChannelSite";
import { PriceStatus, GroupPriceStatus, UsageDetailRows } from "./Sub2apiPricing";
import type { SubUsage } from "./sub2apiPriceCheck";
import type { ModelPrice } from "./types";
import { BillingRate } from "./BillingRate";
import type { ObservedRate } from "./billingRates";

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
  summary_model?: string;
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
  progress?: SyncProgress;
  message: string;
  synced_at: number;
  balance?: SubAccountBalance | null;
  targets: SubTarget[];
}
const pending = (state: string) => state === "queued" || state === "running";
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
    initialData: account.balance || undefined,
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
          ? `余额查询于 ${time(balance.checked_at)}${query.isError || balance.status === "error" ? " · 更新失败，上次结果" : ""}`
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
  if (pending(check.state))
    return (
      <span className="check-status">
        <Spinner small />
        {check.state === "queued" ? "排队中" : "检测中"}
      </span>
    );
  if (check.state === "interrupted" || check.state === "error")
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

function GroupAvailability({ checks }: { checks: SubModelCheck[] }) {
  const counts = availabilityCounts(checks);
  return (
    <div className="sub-availability-summary">
      <span
        className={`check-status ${counts.success ? "pass" : "inconclusive"}`}
      >
        {counts.success}/{checks.length} 可用
      </span>
      <small className="check-source">
        {[
          counts.warning ? `${counts.warning} 个结束异常` : "",
          counts.failed ? `${counts.failed} 个失败` : "",
          counts.untested ? `${counts.untested} 个未检测` : "",
        ]
          .filter(Boolean)
          .join(" · ")}
      </small>
      {counts.pending > 0 && (
        <small className="check-status">
          <Spinner small />
          {counts.pending} 个排队 / 检测中
        </small>
      )}
      {counts.interrupted > 0 && (
        <small className="check-source">{counts.interrupted} 个中断</small>
      )}
    </div>
  );
}

function ModelMatch({ check }: { check: SubModelCheck }) {
  if (pending(check.state) || check.state === "interrupted") return <span className="check-status">{importModelLabel(check)}</span>;
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

function GroupModelMatch({ checks }: { checks: SubModelCheck[] }) {
  const settled = checks.filter(c => !pending(c.state) && c.state !== "interrupted");
  const statuses = settled.map(modelMatchStatus);
  const matches = statuses.filter((s) => s === "match").length;
  return (
    <div className="sub-model-match-summary">
      <span className={`check-status ${matches ? "pass" : "inconclusive"}`}>
        {matches}/{checks.length} 匹配
      </span>
      {checks.length > settled.length && <small className="check-source">{checks.filter(c=>pending(c.state)).length || 0} 个排队 / 检测中{checks.some(c=>c.state === "interrupted") ? " · 含已中断模型" : ""}</small>}
      {(
        [
          "mismatch",
          "missing",
          "invalid",
          "unavailable",
          "legacy",
          "untested",
        ] as const
      ).map((status) => {
        const count = statuses.filter((s) => s === status).length;
        return count ? (
          <small
            className={`check-source ${status === "mismatch" ? "negative" : ""}`}
            key={status}
          >
            {count} 个{modelMatchLabels[status]}
          </small>
        ) : null;
      })}
    </div>
  );
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
    queryFn: async ({ signal }) => {
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
      const fullTarget = data.data[0]?.targets[0];
      if (!fullTarget) throw Error("检测详情暂不可用");
      const fullChecks = modelChecks(fullTarget);
      return {target:fullTarget,checks:fullChecks,selected:fullChecks.find(c=>c.model===model)};
    },
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
                  <UsageDetailRows probe={attempt} label="工具检测" prices={prices} model={attempt.requested_model || toolResult?.model || ""} />
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
              <UsageDetailRows probe={probe} label="可用性" prices={prices} model={check.model} />
              <DetailRow label="最近检测">
                {result ? time(result.checked_at) : "未检测"}
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
                  ) : <UsageDetailRows probe={qualityResult?.quality} label="降智" prices={prices} model={check.model} />}
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
  const complete = useQuery({
    queryKey: ["sub-management-models", account.id, row.target.group_id],
    enabled: !!row.target.summary_model,
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: ({ signal }) => readCompleteSubTarget(account.id, row.target.group_id, signal),
  });
  const target = complete.data || row.target;
  const checks = complete.data ? modelChecks(target) : row.checks;
  const selected = complete.data ? checks.find(check => check.model === row.selected?.model) : row.selected;
  const modelsPending = !!row.target.summary_model && !complete.data;
  const current = selected || checks[0];
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
          <span className="eyebrow">CHANNEL MANAGEMENT</span>
          <Dialog.Title>{target.name}</Dialog.Title>
          <Dialog.Description className="detail-description">
            <span>{account.name}</span>
            <span>·</span>
            <span>{target.channel || target.platform}</span>
            {target.group_id > 0 && <span>· 分组 #{target.group_id}</span>}
          </Dialog.Description>
          <FingerprintPanel targets={fingerprintTargets(row, selected ? [selected.model] : undefined)} revisions={configured ? Object.fromEntries(channelMembers(configured).map(m => [JSON.stringify([m.source_id,m.provider]),m.probe_fingerprint || ""])) : undefined}/>
          <div className="detail-channel-actions">
            <span className={`check-status ${target.state === "error" ? "fail" : "pass"}`}>
              {target.state === "error" ? <X size={15} /> : <Check size={15} />}
              {target.state === "error" ? "同步失败" : target.active ? "可检测" : "未激活"}
            </span>
            <button className="button small primary" onClick={onOpenChannel}>
              {configured ? "查看接入配置" : "添加到渠道"}
            </button>
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
                  const available = check.result?.availability.status === "success";
                  return (
                    <li key={check.model}>
                      {available ? <Check size={15} className="detail-model-check" /> : <CircleHelp size={15} />}
                      <div>
                        <strong>{check.model}</strong>
                        <small>{check.result ? `最近检测 ${time(check.result.checked_at)}` : "尚未检测"}</small>
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
                  <dd><PriceStatus check={current} prices={prices} /></dd>
                </div>
                <div>
                  <dt>最近检测</dt>
                  <dd>{current.result ? time(current.result.checked_at) : "未检测"}</dd>
                </div>
              </dl>
              {(current.result?.availability.message || current.message) && (
                <p className="sub-management-message">{current.result?.availability.message || current.message}</p>
              )}
            </section>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function Sub2apiChecks({ user = "account" }: { user?: string }) {
  const client = useQueryClient();
  const sources = useConsoleSources(user);
  const [filters, setFilters] = useState(() => loadSubFilters(user));
  const [cursorState,setCursorState]=useState({filter:"",cursor:""});
  const filterKey=JSON.stringify(filters);
  const cursor=cursorState.filter===filterKey?cursorState.cursor:"";
  const pageQuery=useManagementPage(user,filters,cursor);
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
  const [syncProgressSubmission,setSyncProgressSubmission]=useState({owner:user,generation:0});
  const syncGeneration=syncProgressSubmission.owner===user?syncProgressSubmission.generation:0;
  const progressQuery=useSubSyncProgress(user,accounts,metadata?.readAt,syncGeneration);
  const awaitingSyncProgress=syncGeneration>0 && !progressQuery.data;
  const statusAccounts=progressQuery.data?.data || accounts;
  const progressByAccount=new Map(statusAccounts.map(a=>[a.id,a]));
  const query={...pageQuery,data:pageData?{data:accounts,cached:false}:undefined};
  const accountHeaders={data:metadata?{data:statusAccounts}:undefined,error:metadata?null:pageQuery.error};
  const prices={data:displayedPage?{data:displayedPage.context.prices}:undefined};
  const [form, setForm] = useState<{ account: SubAccount | null } | null>(null);
  const setPage=(_page:number)=>setCursorState({filter:filterKey,cursor:""});
  const refreshPage=()=>{void progressQuery.refetch();void pageQuery.refetch();};
  const [modelSelectionRevision, setModelSelectionRevision] = useState(0);
  const {
    search,
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
  const [importing, setImporting] = useState<{
    account: SubAccount;
    target: SubTarget;
    configured?: ManagedChannel;
  } | null>(null);
  const dialogImports=useImportBindings(!!importing, importing ? {accountId:importing.account.id,groupId:importing.target.group_id} : undefined);
  const imports={data:displayedPage?{data:displayedPage.context.imports,labels:{},unavailable_sources:[]}:undefined};
  const [error, setError] = useState("");
  const [action, setAction] = useState("");
  const checksInFlight = useRef(new Set<string>());
  const [submittingChecks, setSubmittingChecks] = useState(new Set<string>());
  const [syncSubmitted, setSyncSubmitted] = useState<{count:number;detect:boolean} | null>(null);
  const syncableAccounts = statusAccounts.filter(
    (account) => !pending(account.state),
  );
  const [removing, setRemoving] = useState<string | null>(null);
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
  const allModelsSelected = detectionModels.length === availableModels.length;
  const currentPage=(displayedPage?.offset || 0)/25;
  const selectionID = (account: SubAccount, target: SubTarget, configured?: ManagedChannel) =>
    !account.id && configured ? `configured:${configured.provider}:${configured.engine}` : `${account.id}:${target.group_id}`;
  const [readingSelection,setReadingSelection]=useState(false);
  const selectionRead=useRef(false);
  const selectionRequest=useRef<AbortController|null>(null);
  const selectionScope=useRef("");
  selectionScope.current=JSON.stringify([user,filterKey,cursor,pageData?.revision]);
  useEffect(()=>{
    selectionRequest.current?.abort();
    return ()=>{selectionRequest.current?.abort();};
  },[user,filterKey,cursor]);
  async function completeSelection(){
    if(!pageData)throw Error("请先读取最新渠道结果");
    selectionRequest.current?.abort();
    const controller=new AbortController(),scope=selectionScope.current;
    selectionRequest.current=controller;
    try{
      const selected=await readManagementSelection(filters,pageData,controller.signal);
      if(scope!==selectionScope.current)throw Error("筛选或渠道结果已变化，请重新选择操作");
      return selected;
    }finally{if(selectionRequest.current===controller)selectionRequest.current=null;}
  }
  async function mutate(
    key: string,
    path: string,
    body?: unknown,
    method = "POST",
  ) {
    if (action) return;
    setAction(key);
    setError("");
    try {
      await controlRequest(path, {
        method,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      void client.invalidateQueries({ queryKey: ["sub2api"] });
      await client.invalidateQueries({ queryKey: ["sub2api-headers"] });
      setRemoving(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
    } finally {
      setAction("");
    }
  }
  async function syncAll(detect = true) {
    if (action || awaitingSyncProgress || !syncableAccounts.length || (detect && !detectionModels.length)) return;
    setAction(detect ? "sync-all" : "sync-only");
    setError("");
    setSyncSubmitted(null);
    try {
      const result = await controlRequest<{ queued: number }>(
        detect ? "/v1/sub2api/accounts/sync-selected" : "/v1/sub2api/accounts/sync-only",
        { method: "POST", body: JSON.stringify(detect ? { models: detectionModels } : {}) },
      );
      setSyncSubmitted({count:result.queued,detect});
      setSyncProgressSubmission(previous=>({owner:user,generation:previous.owner===user?previous.generation+1:1}));
    } catch (e) {
      setError(e instanceof ApiError && e.status === 404 ? "同步检测服务待更新，请稍后重试。" : e instanceof Error ? e.message : "批量同步失败");
    } finally {
      setAction("");
    }
  }
  type CheckKind = "check" | "quality" | "compaction" | "tool-use";
  const requestedModels = () => modelsToCheck;
  const checkKeys = (account: SubAccount, target: SubTarget, kind: CheckKind, configured?: ManagedChannel) =>
    (kind === "check" ? requestedModels() : kind === "quality" ? ["gpt-6-astra"] : [kind])
      .map(item => `${selectionID(account, target, configured)}:${item}`);
  const checkBusy = (account: SubAccount, target: SubTarget, kind: CheckKind, configured?: ManagedChannel) => {
    const current=progressByAccount.get(account.id)||account;
    if (account.id && awaitingSyncProgress) return true;
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
  async function queueChecks(kind: CheckKind, selection: CheckSelection) {
    const targets = selection.filter(({ account, target, configured }) => canCheck(account, target, kind, configured));
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
      const requests: Promise<unknown>[] = [];
      if (site.length) requests.push(controlRequest(`/v1/sub2api/${kind === "check" ? "checks" : `${kind}-checks`}`, {
        method:"POST", body:JSON.stringify({targets:site.map(({account,target}) => ({account_id:account.id,group_id:target.group_id,...(kind === "check" ? {models:modelsToCheck} : {})}))}),
      }));
      if (native.length) requests.push(controlRequest("/v1/channel-management/checks", {method:"POST",body:JSON.stringify({kind,targets:native})}));
      const results = await Promise.allSettled(requests);
      await Promise.all([client.invalidateQueries({queryKey:["sub2api"]}),client.invalidateQueries({queryKey:["configured-checks"]})]);
      const failed = results.find(r => r.status === "rejected");
      if (failed?.status === "rejected") throw failed.reason;
    } catch (e) {
      setError(e instanceof Error ? e.message : "检测提交失败");
    } finally {
      keys.forEach(key => checksInFlight.current.delete(key));
      setSubmittingChecks(new Set(checksInFlight.current));
    }
  }
  async function batch(kind:CheckKind){
    if(selectionRead.current || !pageData)return;
    selectionRead.current=true;setReadingSelection(true);setError("");
    try{const selected=await completeSelection();await queueChecks(kind,selected.eligible);}
    catch(e){setError(e instanceof Error?e.message:"完整筛选结果读取失败");}
    finally{selectionRead.current=false;setReadingSelection(false);}
  }
  const batchBusy=(_kind:CheckKind)=>readingSelection || submittingChecks.size>0;
  const batchDisabled=(kind:CheckKind)=>resultsUnavailable || !eligibleCount || readingSelection || submittingChecks.size>0 || (kind==="check"&&!modelsToCheck.length) || ((kind==="compaction"||kind==="tool-use")&&!!model&&!isCapabilityCheckModel(model)) || (rows.length===total && !rows.some(row=>canCheck(row.account,row.target,kind,row.configured)));
  const check=(selection?:CheckSelection)=>selection?queueChecks("check",selection):batch("check");
  const checkQuality=()=>batch("quality");
  const checkCompaction=(selection?:CheckSelection)=>selection?queueChecks("compaction",selection):batch("compaction");
  const checkToolUse=(selection?:CheckSelection)=>selection?queueChecks("tool-use",selection):batch("tool-use");
  return (
    <div className="sub2api-page">
      <section className="data-panel sub-accounts">
        <div className="data-heading">
          <div className="data-title">
            <Globe2 size={19} />
            <h2>站点账号</h2>
            <span className="count-badge">{!query.data && !accountHeaders.data ? "…" : statusAccounts.length}</span>
          </div>
          <div className="sub-account-actions">
            <button
              className="button small"
              aria-label="一键同步并检测"
              title={!detectionModels.length ? "请在检测模型设置中选择模型" : `仅检测已勾选的 ${detectionModels.length} 个模型。可用的目标模型同时检测模型指纹。${fingerprintNote}`}
              aria-busy={action === "sync-all"}
              disabled={
                !syncableAccounts.length ||
                !detectionModels.length ||
                awaitingSyncProgress ||
                !!action ||
                query.isPending ||
                query.isError
              }
              onClick={() => void syncAll()}
            >
              {action === "sync-all" ? (
                <Spinner small />
              ) : (
                <RefreshCw size={15} />
              )}
              {action === "sync-all"
                ? "正在提交…"
                : `一键同步并检测 · ${syncableAccounts.length} 个账号`}
            </button>
            <button className="button small" aria-label="一键同步" title="仅同步账号、分组和密钥信息，不执行模型或能力检测"
              aria-busy={action === "sync-only"}
              disabled={!syncableAccounts.length || awaitingSyncProgress || !!action || query.isPending || query.isError}
              onClick={()=>void syncAll(false)}>
              {action === "sync-only" ? <Spinner small/> : <RefreshCw size={15}/>}
              {action === "sync-only" ? "正在提交…" : `一键同步 · ${syncableAccounts.length} 个账号`}
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
        {syncSubmitted !== null && (
          <p className="settings-note" role="status">
            {syncSubmitted.count
              ? `已提交 ${syncSubmitted.count} 个账号，${syncSubmitted.detect ? "正在后台同步并检测。" : "正在后台同步，不执行检测。"}`
              : "暂无可同步账号，已有任务会继续执行。"}
          </p>
        )}
        {modelSelectionNeedsReview && (
          <p className="settings-note" role="status">
            检测模型设置已安全迁移：旧设置无法确认额外模型是否在保存时已存在，因此额外模型暂未勾选。请检查检测模型设置后再运行批量检测。
          </p>
        )}
        {syncSubmitted !== null && <p className="settings-note">任务进度实时更新。渠道列表保留当前展示，点击“刷新渠道管理”读取最新检测结果。</p>}
        {form && (
          <AccountForm
            key={form.account?.id || "new"}
            initial={form.account}
            close={() => setForm(null)}
            saved={() => {
              setForm(null);
              void client.invalidateQueries({ queryKey: ["sub2api"] });
              void client.invalidateQueries({ queryKey: ["sub2api-headers"] });
            }}
          />
        )}
        {progressQuery.isError && <p role="alert" className="settings-note">实时进度更新失败，当前进度尚未确认。<button className="button small" onClick={()=>void progressQuery.refetch()}>重试进度</button></p>}
        {accountHeaders.error && (
          <div role="alert" className="error-banner">
            账号状态读取失败：{accountHeaders.error.message}
            <button
              className="button small"
              onClick={refreshPage}
            >
              重试
            </button>
          </div>
        )}
        {!metadata && resultsReadFailed ? null : !metadata ? (
          <TableLoading kind="accounts" rows={3} />
        ) : statusAccounts.length === 0 ? (
          <Empty title="添加第一个站点账号" icon={<Globe2 size={25} />}>
            填入站点地址与账号密码，自动发现可用分组并检测模型。
          </Empty>
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
                        {a.sync_only && pending(a.state) ? (a.state === "queued" ? "等待同步" : "同步中") : stateLabel[a.state] || a.state}
                        {total > 0 && ` · ${completed}/${total} 个分组`}
                      </span>
                      {a.sync_only && pending(a.state) ? <small>仅同步信息，不执行检测</small> : null}
                      {!a.sync_only && a.sync_models?.length && pending(a.state) && a.job_kind === "sync" ? <small>本次同步选择 {a.sync_models.length} 个模型 / 分组</small> : null}
                      {a.progress && !a.sync_only && <small>模型进度：{a.progress.completed}/{a.progress.total} 完成 · {a.progress.running} 检测中 · {a.progress.queued} 排队{a.progress.interrupted ? ` · ${a.progress.interrupted} 已中断` : ""}</small>}
                      {!a.sync_only && a.progress?.running_models.length ? <small>正在检测：{[...new Set(a.progress.running_models)].join("、")}</small> : null}
                      {a.synced_at > 0 && !pending(a.state) && (
                        <small>上次同步 {time(a.synced_at)}</small>
                      )}
                      {accountMessage && (
                        <small className="sub-account-message" title={accountMessage}>{accountMessage}</small>
                      )}
                    </div>
                    <div className="sub-account-actions">
                      {pending(a.state) ? (
                        <button
                          className="button small"
                          disabled={!!action}
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
                            disabled={!!action}
                            onClick={() =>
                              void mutate(
                                a.id,
                                `/v1/sub2api/accounts/${a.id}/sync`,
                              )
                            }
                          >
                            <RefreshCw size={13} />
                            同步并检测
                          </button>
                          <button
                            className="button ghost small"
                            onClick={() => setForm({ account: a })}
                          >
                            重新登录
                          </button>
                          <button
                            className="icon-button"
                            aria-label={`移除账号 ${a.name}`}
                            disabled={!!action}
                            onClick={() => setRemoving(a.id)}
                          >
                            <Trash2 size={15} />
                          </button>
                        </>
                      )}
                    </div>
                    {removing === a.id && (
                      <div className="sub-remove" role="alert">
                        移除后删除本地凭据和检测记录；上游测试 key
                        保留，可在站点密钥页面撤销。
                        <button
                          className="button small"
                          disabled={!!action}
                          onClick={() =>
                            void mutate(
                              a.id,
                              `/v1/sub2api/accounts/${a.id}`,
                              undefined,
                              "DELETE",
                            )
                          }
                        >
                          确认移除
                        </button>
                        <button
                          className="button small"
                          onClick={() => setRemoving(null)}
                        >
                          取消
                        </button>
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          </div>
        )}
      </section>
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      <section className="data-panel">
        <div className="data-heading sub-detection-heading">
          <div className="sub-detection-title">
            <div className="data-title">
              <ScanLine size={19} />
              <h2>渠道管理</h2>
              <span className="count-badge">{resultsUnavailable ? "—" : total}</span>
            </div>
            <span className="sub-detection-scope">{resultsUnavailable ? "" : `${eligibleCount} 个渠道可检测`}</span>
          </div>
          <div className="sub-detection-controls" role="region" aria-label="渠道管理操作" tabIndex={0}>
            <CreateChannel sources={sources.data?.data || []} />
            <ChannelOptimization
              disabled={!pageData || !total || readingSelection}
              snapshot={async()=>managementOptimizationSnapshot(await completeSelection(),filters)}
            />
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
            <div className="sub-check-group" role="group" aria-label="专项检测">
              <button
                className="button small"
                aria-label={`降智检测 · ${eligibleCount} 个渠道`}
                disabled={batchDisabled("quality")}
                onClick={() => void checkQuality()}
              >
                {batchBusy("quality") ? (
                  <Spinner small />
                ) : (
                  <ScanLine size={15} />
                )}
                降智检测
              </button>
              <button className="button small" disabled={batchDisabled("compaction")}
                title={capabilityCheckScope} aria-label={`压缩检测 · ${eligibleCount} 个渠道`}
                onClick={() => void checkCompaction()}>
                {batchBusy("compaction") ? <Spinner small /> : <ScanLine size={15} />} 压缩检测
              </button>
              <button className="button small" disabled={batchDisabled("tool-use")}
                aria-label={`Tool use 检测 · ${eligibleCount} 个渠道`}
                onClick={() => void checkToolUse()} title={toolUseDescription}>
                {batchBusy("tool-use") ? <Spinner small /> : <ScanLine size={15} />} Tool use
              </button>
            </div>
            <button
              className="button small sub-check-icon"
              aria-label="刷新渠道管理"
              title="刷新数据"
              onClick={refreshPage}
              disabled={query.isFetching}
            >
              <RefreshCw size={15} className={query.isFetching ? "spin" : ""} />
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
          </div>
        </div>
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
              <option value="">默认顺序</option>
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
              setFilters({ ...subFilterDefaults });
              setPage(0);
            }}
          >
            <X size={12} /> 重置筛选
          </button>
        </div>
        {resultsReadFailed && metadata && <div role="alert" className="error-banner">渠道结果读取失败：{pageQuery.error?.message}<button className="button small" onClick={refreshPage}>重试</button></div>}
        {displayedPage && <div className="sub-results-status" role="status" aria-live="polite">
          {pageQuery.updating ? <><Spinner small/>正在更新渠道结果，暂显示更新前内容…</> : resultsReadFailed ? "暂时无法读取渠道" : ""}
        </div>}
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
              <table className="channel-table check-table sub-table">
                <thead>
                  <tr>
                    <th>渠道 / 站点</th>
                    {tableModel && <th>模型</th>}
                    <th>平台 / key</th>
                    <th>倍率</th>
                    <th>可用性</th>
                    <th>首字延迟</th>
                    <th>
                      <Tip text="比较检测请求的模型名与流式完成事件中的 response.model，完全一致才算匹配。独立降智检测同时更新 Astra 可用性和模型匹配，不额外发起请求。">
                        模型匹配 <CircleHelp size={12} />
                      </Tip>
                    </th>
                    <th><Tip text="按站点请求账单的倍率前输入／输出单价与价格设置比较；单位为美元／百万 token。缺少账单或 token 样本时不判为正常。">单价异常 <CircleHelp size={12} /></Tip></th>
                    <th><Tip text={fingerprintNote}>模型指纹 <CircleHelp size={12}/></Tip></th>
                    <th>Astra 降智</th>
                    <th>远程压缩</th>
                    <th><Tip text={toolUseDescription}>Tool use <CircleHelp size={12} /></Tip></th>
                    <th>回复 / 诊断</th>
                    <th>最近检测</th>
                    <th>操作</th>
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
                      const liveGroup = progressByAccount.get(account.id)?.progress?.groups.find(g=>g.group_id===t.group_id);
                      const scopeLabel = scopedModels?.length ? `本次已选 ${overviewChecks.length} 个模型` : `全部 ${checks.length} 个模型（含历史记录）`;
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
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            openDrawer();
                          }
                        }}
                        aria-label={`查看 ${account.name} ${t.name} 渠道详情`}
                      >
                        <td>
                          <strong>{t.name}</strong>
                          <small className="check-source">
                            {account.name}
                            {t.channel ? ` · ${t.channel}` : ""}
                          </small>
                          <small className="check-source">
                            {configured ? `${configured.source_name} · 已有渠道` : `分组 #${t.group_id}`}
                            {!configured && !t.active && " · 已不可用"}
                          </small>
                        </td>
                        {tableModel && <td className="mono">{tableModel}</td>}
                        <td>
                          {t.platform}
                          <small className="check-source">
                            {t.key_id ? `key #${t.key_id}` : configured ? "配置密钥" : "尚未创建"}
                          </small>
                          <ChannelProtocolSummary row={managementRow} imports={imports.data} routes={optimizationRoutes}/>
                        </td>
                        <td><BillingRate target={t} model={tableModel} configured={configured} /></td>
                        <td>
                          {t.state === "error" ? (
                            <span className="check-status error">同步失败</span>
                          ) : selected ? (
                            <AvailabilityStatus check={selected} />
                          ) : (
                            <><small className="check-source">{scopeLabel}</small>
                            {scopedModels?.length ? <small className="check-source">全部 {checks.length} 个模型记录仍可在详情查看</small> : null}
                            {liveGroup && scopedModels?.length ? <>
                              <small className="check-status">本次进度：{liveGroup.completed}/{liveGroup.total} 完成 · {liveGroup.running} 检测中 · {liveGroup.queued} 排队{liveGroup.interrupted ? ` · ${liveGroup.interrupted} 已中断` : ""}</small>
                              <small className="check-source">刷新渠道查看最新检测结果</small>
                            </> : <GroupAvailability checks={overviewChecks} />}</>
                          )}
                        </td>
                        <td className="mono">
                          <ResponseLatency
                            protocol={selected?.result?.availability.protocol}
                            firstResponse={selected?.result?.availability.first_response_ms}
                            created={
                              selected?.result?.availability.response_created_ms
                            }
                            text={selected?.result?.availability.ttft_ms}
                          />
                        </td>
                        <td>
                          {selected ? (
                            <ModelMatch check={selected} />
                          ) : (
                            <><small className="check-source">{scopeLabel}</small>{liveGroup && scopedModels?.length ? <small className="check-source">已完成 {liveGroup.completed}/{liveGroup.total} · 刷新渠道查看匹配结果</small> : <GroupModelMatch checks={overviewChecks} />}</>
                          )}
                        </td>
                        <td>{selected ? <PriceStatus check={selected} prices={prices.data?.data} /> : <GroupPriceStatus checks={checks} prices={prices.data?.data} />}</td>
                        <td><FingerprintCell entries={(displayedPage?.context.fingerprints[id] || []).map(({target,record})=>({target,record}))} supportedModels={displayedPage?.context.fingerprint_models}/></td>
                        <td>
                          <div className="quality-status-stack"><Verdict result={groupQualityResult(t)} history={t.history} /><QualityProbability history={t.history} checkedAt={groupQualityResult(t)?.checked_at} /></div>
                        </td>
                        <td><div className="quality-status-stack"><CompactionStatus target={t} model={tableModel || undefined} /><button className="button small ghost" disabled={!canCheck(account, t, "compaction", configured)} aria-busy={checkBusy(account, t, "compaction", configured)}
                          title={capabilityCheckScope} aria-label={`检测 ${account.name} ${t.name} 的远程压缩`} onClick={() => void checkCompaction([{account, target:t, configured}])}>{checkBusy(account, t, "compaction", configured) && <Spinner small />}重新检测</button></div></td>
                        <td><div className="quality-status-stack"><ToolUseStatus target={t} model={tableModel || undefined} /><button className="button small ghost" disabled={!canCheck(account, t, "tool-use", configured)} aria-busy={checkBusy(account, t, "tool-use", configured)}
                          title={capabilityCheckScope} aria-label={`检测 ${account.name} ${t.name} 的 Tool use`} onClick={() => void checkToolUse([{account, target:t, configured}])}>{checkBusy(account, t, "tool-use", configured) && <Spinner small />}重新检测</button></div></td>
                        <td>
                          {<CheckDetails
                            key={tableModel || "all"}
                            account={account}
                            target={t}
                            checks={checks}
                            selected={selected}
                            prices={prices.data?.data}
                            configured={configured}
                          />}
                        </td>
                        <td className="mono">
                          {(() => {
                            const at =
                              selected?.result?.checked_at ??
                              (selected
                                ? 0
                                : Math.max(
                                    0,
                                    ...checks.map(
                                      (c) => c.result?.checked_at || 0,
                                    ),
                                  ));
                            return at ? time(at) : "—";
                          })()}
                          {selected &&
                            pending(selected.state) &&
                            selected.result && (
                              <small className="check-source">上次结果</small>
                            )}
                        </td>
                        <td>
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
                  onClick={() => setCursorState({filter:filterKey,cursor:pageData?.previous_cursor || ""})}
                >
                  上一页
                </button>
                <span>
                  {currentPage + 1} / {Math.max(1, Math.ceil(total / 25))}
                </span>
                <button
                  className="button small"
                  disabled={!pageData?.next_cursor}
                  onClick={() => setCursorState({filter:filterKey,cursor:pageData?.next_cursor || ""})}
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </section>
      {importing && (
        <Sub2apiImport
          sources={sources}
          imports={dialogImports}
          account={importing.account}
          configured={importing.configured}
          target={accounts.find(a => a.id === importing.account.id)?.targets.find(t => t.group_id === importing.target.group_id) || importing.target}
          prices={prices.data?.data}
          close={() => setImporting(null)}
        />
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
