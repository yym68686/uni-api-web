import type { ReactNode } from "react";
import { useId, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Check, ChevronDown, CircleHelp, Fingerprint, Play, RefreshCw, X } from "./icons";
import { ApiError, controlRequest } from "./api";
import { channelMembers, useChannelManagement } from "./channelManagement";
import type { ManagementRow } from "./channelManagement";
import { CompactDialogHeader } from "./CompactDialog";
import type { Channel } from "./types";
import { rate, time } from "./format";
import { useSubImports } from "./sub2apiImports";
import { Spinner, Tip } from "./ui";
import { LoadingIndicator } from "./LoadingIndicator";
import { fingerprintKey, sharedFingerprintIndex } from "./fingerprintResults";
import { fingerprintFailures, fingerprintSampleFailure, observedGroupRejections } from "./fingerprintRejections";
import { provisionalProbabilities } from "../analytics-api/fingerprint/provisional-probabilities";
import referenceModels from "../analytics-api/fingerprint/models.json";
export { fingerprintKey } from "./fingerprintResults";

const legacyFingerprintModels = ["gpt-6-astra", "gpt-6.1-sol"];
export const FINGERPRINT_BATCH_LIMIT = 512;
export const fingerprintLabels: Record<string, string> = {
  match: "匹配", mismatch: "不匹配", insufficient: "样本不足", error: "检测失败",
  queued: "排队中", running: "检测中", interrupted: "已中断", untested: "未检测", not_applicable: "不适用",
};
export const fingerprintNote = "每轮累计 3 个有效样本；1、2 个样本时可查看候选排名及未校准的暂估概率，满 3 个后计算正式匹配结果与概率。不足 3 个时保留已有样本，再次检测只补缺少的样本。满 3 个后再次检测开启新一轮；渠道配置或参考库变化时重新采样。Astra 与 6.1-sol 共用一个指纹类别；Gemini 3.1 Pro 对应 3.1 Pro Preview，3.8 Flash、4 Argon 和 4 Carbon 各自独立。DeepSeek V4.1 Flash 1017 使用独立类别及 low 推理强度，不与未标注版本的 Flash 共用身份。概率是相对估计，不代表身份保证或不降智概率。";
const partialNote = "满 3 个有效样本后计算正式匹配结果与概率。";
const nonModelNote = "数字序列特征不符合参考库要求，无法判定模型身份；未生成排名或匹配概率。";
export interface FingerprintTarget {
  source_id?: string; provider?: string; account_id?: string; group_id?: number; model: string;
}
export interface FingerprintResult {
  model: string; status: string; probability: number | null; decision?: "partial" | "scored" | "unscorable" | "not_model";
  estimated_probability?: number | null; estimated_probability_scope?: "reference-closed-set";
  prediction: string; prediction_name: string; expected_class: string; expected_class_id?: string;
  used_outputs: number; checked_at: number; reference_commit: string;
  candidates: { model: string; display_name: string; probability: number | null; score?: number; estimated_probability?: number }[];
  samples?: { request_id: string; state: string; message?: string; http_status?: number; error_code?: string }[];
}
export interface FingerprintRecord extends FingerprintTarget {state: string; message: string; revision: string; updated_at_ms?: number; retained_samples?: number; result: FingerprintResult | null; previous_result?: FingerprintResult | null; previous_revision?: string}
export const fingerprintReadMessage = (error: unknown) => error instanceof ApiError && error.status === 404 ? "指纹检测服务待更新" : "模型指纹读取失败";
export const fingerprintBusy = (record?: FingerprintRecord) => record?.state === "queued" || record?.state === "running";
export function fingerprintStatus(record?: FingerprintRecord) {
  return !record ? "untested" : record.state !== "done" ? record.state : record.result?.status || "error";
}
export function fingerprintVerdict(record?: FingerprintRecord) {
  const status = fingerprintStatus(record);
  return status === "match" || status === "mismatch" ? record?.result : record?.previous_result;
}

export function useSiteFingerprints(account: string, group: number, enabled: boolean) {
  return useQuery({
    queryKey: ["model-fingerprints", "site", account, group],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ account_id: account, group_id: String(group) });
      const { data } = await controlRequest<{data: FingerprintRecord[]}>(`/v1/model-fingerprints?${query}`, { signal });
      return new Map((Array.isArray(data) ? data : [])
        .filter(record => record.account_id === account && record.group_id === group)
        .map(record => [record.model, record]));
    },
    enabled: enabled && !!account && group > 0,
    staleTime: 5000,
    retry: false,
  });
}
function partialFingerprint(result?: FingerprintResult | null) {
  return result?.decision === "partial" && result.used_outputs > 0 && result.used_outputs < 3 && result.candidates?.length > 0;
}
export function fingerprintImportMismatch(record?: FingerprintRecord): "confirmed" | "provisional" | undefined {
  const verdict = fingerprintVerdict(record);
  if (verdict?.status === "mismatch") return "confirmed";
  if (verdict || !partialFingerprint(record?.result) || !record?.result?.prediction) return undefined;
  const legacy = (referenceModels as Record<string, {class_id:string;class_name:string}>)[record.result.model];
  const expected = record.result.expected_class_id ||
    (legacy && legacy.class_name === record.result.expected_class ? legacy.class_id : undefined);
  return expected && record.result.prediction !== expected ? "provisional" : undefined;
}
function fingerprintLabel(status: string, result?: FingerprintResult | null) {
  if (status === "error") {
    const failures = fingerprintFailures(result);
    return failures.at(-1)?.label || fingerprintLabels.error;
  }
  if (result?.decision === "not_model") return "无法判定";
  return fingerprintLabels[status] || "检测失败";
}
export function fingerprintProbabilityClass(p: number | null) {
  return p === null ? "quality-unknown" : p === 1 ? "quality-perfect" : p >= .9 ? "quality-partial" : "quality-poor";
}
type VerdictKind = "match" | "mismatch";
export function FingerprintVerdictBadge({kind, probability, prefix}: {kind:VerdictKind; probability:number|null; prefix?:string}) {
  return <span className={`fingerprint-verdict ${kind}`}>
    {kind === "match" ? <Check size={12}/> : <X size={12}/>}
    {prefix}{fingerprintLabels[kind]}
    <small className={`quality-probability ${fingerprintProbabilityClass(probability)}`} title={probability == null ? "无可用匹配概率" : "请求模型的匹配概率"}>{rate(probability)}</small>
  </span>;
}
export function fingerprintTargets(row: Pick<ManagementRow, "account" | "target" | "configured">, models: string[]): FingerprintTarget[] {
  const supported = [...new Set(models)];
  if (row.account.id) return supported.map(model => ({ account_id: row.account.id, group_id: row.target.group_id, model }));
  if (!row.configured) return [];
  const members = channelMembers(row.configured);
  return supported.map(model => {
    const member = members.find(m => m.models.includes(model)) || members[0];
    return { source_id: member.source_id, provider: member.provider, model };
  });
}
export function rowFingerprints(row: ManagementRow, index: Map<string, FingerprintRecord>, model: string, supportedModels: string[]) {
  return fingerprintTargets(row, model ? supportedModels.filter(name => name === model) : supportedModels).map(target => {
    let record = index.get(fingerprintKey(target));
    if (record?.state === "done" && target.source_id && fingerprintKey(record) === fingerprintKey(target) && row.configured) {
      const member = channelMembers(row.configured).find(m => m.source_id === target.source_id && m.provider === target.provider);
      if (member?.probe_fingerprint !== record.revision) record = undefined;
    }
    return { target, record };
  });
}
export function observedFingerprints(row: Channel, index: Map<string, FingerprintRecord>, member: ManagementRow["configured"] | undefined, supportedModels: string[]) {
  const target = { source_id: row.source_id, provider: row.provider, model: row.model };
  let record = index.get(fingerprintKey(target));
  if (!supportedModels.includes(row.model) && !record) return [];
  // Observation can include historical routes. Reuse only the current model
  // mapping and credential evidence, just like the management/detail views.
  if (!member?.models?.includes(row.model) || (member.model_mappings?.[row.model] || row.model) !== row.upstream_model ||
      (record?.state === "done" && fingerprintKey(record) === fingerprintKey(target) && record.revision !== member.probe_fingerprint)) record = undefined;
  return [{ target, record }];
}
export function useModelFingerprints(enabled = true) {
  const management = useChannelManagement(enabled);
  const imports = useSubImports(enabled);
  const query = useQuery({
    queryKey: ["model-fingerprints"],
    queryFn: ({ signal }) => controlRequest<{data: FingerprintRecord[]; supported_models?: string[]}>("/v1/model-fingerprints", { signal }),
    enabled,
    staleTime: 5000, refetchInterval: q => !enabled ? false : q.state.data?.data?.some(fingerprintBusy) ? 5000 : 15000, retry: false,
  });
  const members = useMemo(() => new Map(
    (!management.isError ? management.data?.data || [] : []).flatMap(channelMembers)
      .filter(member => !(management.data?.unavailable_sources || []).some(source => source === member.source_id || source === member.source_name))
      .map(member => [JSON.stringify([member.source_id, member.provider]), member]),
  ), [management.data, management.isError]);
  const index = useMemo(() => sharedFingerprintIndex(
    Array.isArray(query.data?.data) ? query.data.data : [],
    !management.isError && Array.isArray(management.data?.data) ? management.data.data : [],
    management.data?.unavailable_sources || [],
    !imports.isError && Array.isArray(imports.data?.data) ? imports.data.data : [],
    imports.data?.unavailable_sources || [],
  ), [query.data, management.data, management.isError, imports.data, imports.isError]);
  // Older backends have no capability list and only accept the original pair.
  const supportedModels = Array.isArray(query.data?.supported_models) ? query.data.supported_models : query.data ? legacyFingerprintModels : [];
  const groupRejections = useMemo(() =>
    observedGroupRejections(!query.isError && Array.isArray(query.data?.data) ? query.data.data : [], members),
    [members, query.data, query.isError],
  );
  return { ...query, index, members, supportedModels, groupRejections };
}
function fingerprintPresentation(record?: FingerprintRecord, unsupported = false) {
  const status = fingerprintStatus(record);
  const result = record?.result;
  const verdict = fingerprintVerdict(record);
  const partial = partialFingerprint(result) ? result : undefined;
  const legacy = result && (referenceModels as Record<string, {class_id:string;class_name:string}>)[result.model];
  const expected = result?.expected_class_id ||
    (legacy && legacy.class_name === result?.expected_class ? legacy.class_id : undefined);
  const provisional: VerdictKind | undefined = !verdict && partial && result?.prediction && expected
    ? (result.prediction === expected ? "match" : "mismatch") : undefined;
  const kind: VerdictKind | undefined = verdict?.status === "match" || verdict?.status === "mismatch" ? verdict.status : provisional;
  const message = [...new Set([record?.message, ...fingerprintFailures(result).map(failure => failure.detail)].filter(Boolean))].join("；");
  return { kind, prefix: verdict && verdict === record?.previous_result ? "上次" : provisional ? "暂判" : undefined,
    status, provisional, probability: verdict?.probability ?? null, candidates: verdict?.candidates ?? [], partial,
    label: unsupported ? "待更新" : fingerprintLabel(status, result), message };
}

function FingerprintReadout({label, probability = null, detail, disclosure}: {
  label: ReactNode; probability?: number | null; detail?: ReactNode; disclosure?: boolean;
}) {
  return <span className="fingerprint-readout">
    <span className="fingerprint-detail-status">
      <span className="fingerprint-status-line">{label}{disclosure && <ChevronDown size={12} aria-hidden="true"/>}</span>
      {detail && <small className="fingerprint-attempt muted">{detail}</small>}
    </span>
    <small className={`fingerprint-detail-probability quality-probability ${fingerprintProbabilityClass(probability)}`}>{rate(probability)}</small>
  </span>;
}

function FingerprintIndicator({presentation, disclosure}: {presentation: ReturnType<typeof fingerprintPresentation>; disclosure?: boolean}) {
  const {kind, prefix, status, label, probability} = presentation;
  const Icon = kind === "match" ? Check : X;
  return <FingerprintReadout disclosure={disclosure} probability={probability}
    label={kind ? <span className={`fingerprint-verdict ${kind}`}><Icon size={12} aria-hidden="true"/>{prefix}{fingerprintLabels[kind]}</span> : <span className="muted">{label}</span>}
    detail={kind && prefix && <>{(status === "queued" || status === "running") && <Spinner small/>}{label}</>}/>;
}

export function FingerprintColumnHeader({help, label = "指纹"}: {help: string; label?: string}) {
  return <span className="fingerprint-cell fingerprint-column-heading">
    <span className="fingerprint-readout">
      <Tip text={help}>{label} <CircleHelp size={12} aria-hidden="true"/></Tip>
      <span className="fingerprint-detail-probability">概率</span>
    </span>
  </span>;
}

type FingerprintSummary = {target: FingerprintTarget; presentation: ReturnType<typeof fingerprintPresentation>};

function validEstimate(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 1;
}

function partialEstimates(partial: FingerprintResult) {
  if (partial.estimated_probability_scope === "reference-closed-set") {
    return {
      values: partial.candidates.map(candidate => validEstimate(candidate.estimated_probability) ? candidate.estimated_probability : undefined),
      requested: validEstimate(partial.estimated_probability) ? partial.estimated_probability : undefined,
      note: "暂估概率 · 全参考库归一化，未校准。",
    };
  }
  // Older checkpoints persist the top scores, but not the rest of the bank.
  const values = provisionalProbabilities(partial.candidates.map(candidate => candidate.score));
  const index = partial.candidates.findIndex(candidate => candidate.model === partial.expected_class_id);
  return {
    values,
    requested: values?.[index],
    note: values ? "旧记录仅按已保存候选归一化，暂估概率未校准。" : "此记录没有可用的暂估概率。",
  };
}

function FingerprintCandidateRanking({partial}: {partial: FingerprintResult}) {
  const {values, requested, note} = partialEstimates(partial);
  return <>
    {requested != null && <div>请求模型的暂估概率：{rate(requested)}</div>}
    <div className="fingerprint-tip-heading">本轮候选暂定排名 · {partial.used_outputs}/3 有效样本</div>
    <ol className="fingerprint-candidate-ranking">{partial.candidates.map((candidate, i) => <li key={candidate.model}>
      <span className="fingerprint-candidate-readout">
        <span>{candidate.display_name || candidate.model}</span>
        <span className="fingerprint-candidate-probability">{rate(values?.[i])}</span>
      </span>
    </li>)}</ol>
    <div className="muted">{note} {partialNote}</div>
  </>;
}

function FingerprintProbabilityTip({item}: {item: FingerprintSummary}) {
  const {target, presentation: {probability, candidates, prefix, label, message, partial}} = item;
  return <div className="fingerprint-probability-tip">
    <strong>{target.model}</strong>
    {(!partial || prefix === "上次") && <div>{prefix === "上次" ? "上次请求模型的匹配概率" : "请求模型的匹配概率"}：{rate(probability)}</div>}
    {candidates.length > 0 && <>
      <div className="muted">{prefix === "上次" ? "上次参考库候选概率" : "参考库候选概率"}</div>
      <dl>{candidates.map(candidate => <div key={candidate.model}>
        <dt>{candidate.display_name || candidate.model}</dt><dd>{rate(candidate.probability)}</dd>
      </div>)}</dl>
    </>}
    {partial && <FingerprintCandidateRanking partial={partial}/>}
    {!partial && probability == null && <div className="muted">{label === "样本不足" ? "满 3 个有效样本后计算概率。" : `${label}，暂无可用匹配概率。`}</div>}
    {message && <div className="muted">{message}</div>}
  </div>;
}

function FingerprintDetailRow({item}: {item: FingerprintSummary}) {
  const {target, presentation} = item;
  return <Tooltip.Root>
    <Tooltip.Trigger asChild>
      <li className="fingerprint-result-row fingerprint-detail-row" tabIndex={0}>
        <strong className="fingerprint-model-name">{target.model}</strong>
        <FingerprintIndicator presentation={presentation}/>
      </li>
    </Tooltip.Trigger>
    <Tooltip.Portal>
      <Tooltip.Content className="tooltip" align="end" sideOffset={6} collisionPadding={12}>
        <FingerprintProbabilityTip item={item}/>
        <Tooltip.Arrow className="tooltip-arrow"/>
      </Tooltip.Content>
    </Tooltip.Portal>
  </Tooltip.Root>;
}

export function FingerprintStatus({ record }: { record?: FingerprintRecord }) {
  const presentation = fingerprintPresentation(record);
  return <span className="fingerprint-indicator" title={presentation.message || undefined}><FingerprintIndicator presentation={presentation}/></span>;
}
export function FingerprintCell({ entries, error, loading, supportedModels = [], channel }: { entries: readonly {target: FingerprintTarget; record?: FingerprintRecord}[]; error?: Error | null; loading?: boolean; supportedModels?: string[]; channel?: string }) {
  if (entries.length && error) return <span className="fingerprint-cell"><FingerprintReadout label={<Tip text={fingerprintReadMessage(error)}><span className="muted">读取失败</span></Tip>}/></span>;
  if (entries.length && loading) return <span className="fingerprint-cell"><FingerprintReadout label={<LoadingIndicator size="inline" label="读取模型指纹"/>}/></span>;
  if (!entries.length) return <span className="fingerprint-cell" aria-label="无指纹数据"><FingerprintReadout label={<span className="muted">—</span>}/></span>;
  const summaries = entries.map(({target, record}) => {
    const presentation = fingerprintPresentation(record, !record && !supportedModels.includes(target.model));
    const updatedAt = record?.updated_at_ms ?? (record?.result?.checked_at || 0) * 1000;
    return {target, presentation, updatedAt, recorded: !!record};
  });
  const recorded = summaries.filter(item => item.recorded);
  // The all-model view includes untested candidates; those aren't fingerprint results.
  const visible = recorded.length ? recorded : summaries;
  const [first] = visible;
  const mismatch = visible.some(({presentation:p}) => p.kind === "mismatch" && !p.provisional);
  const label = visible.every(item => item.presentation.label === first.presentation.label) && !mismatch
    ? first.presentation.label : mismatch ? "有不匹配" : "多种状态";
  const disclosure = summaries.length > 1;
  const summary = visible.length === 1 ? <FingerprintIndicator presentation={first.presentation} disclosure={disclosure}/> :
    <FingerprintReadout disclosure label={<span className={label === "匹配" ? "check-status pass" : mismatch ? "check-status fail" : "muted"}>{label}</span>}
      detail={recorded.length > 1 ? `${recorded.length} 个模型有记录` : undefined}/>;
  if (summaries.length === 1) return <Tip text={<>
    <div className="fingerprint-tooltip-status"><FingerprintIndicator presentation={first.presentation}/></div>
    <FingerprintProbabilityTip item={first}/>
  </>}>
    <span className="fingerprint-cell">{summary}</span>
  </Tip>;
  const unrecorded = summaries.filter(item => !item.recorded);
  const recordedRecentFirst = recorded.sort((a, b) =>
    b.updatedAt - a.updatedAt ||
    a.target.model.localeCompare(b.target.model),
  );
  return <Dialog.Root>
    <Dialog.Trigger type="button" className="fingerprint-cell fingerprint-details-trigger">
      {summary}<span className="sr-only">查看{channel ? ` ${channel} 的` : ""}模型指纹</span>
    </Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" onClick={event => event.stopPropagation()} />
      <Dialog.Content className="guide-dialog compact-dialog fingerprint-details-dialog" onClick={event => event.stopPropagation()}>
        <CompactDialogHeader context={channel || `共 ${summaries.length} 个模型`}>模型指纹详情</CompactDialogHeader>
        <div className="compact-dialog-scroll fingerprint-details-scroll" tabIndex={0} role="region" aria-label="模型指纹列表">
          <div className="fingerprint-result-row fingerprint-list-heading" aria-hidden="true"><span>模型</span><span className="fingerprint-readout"><span>状态</span><span className="fingerprint-detail-probability">概率</span></span></div>
          {recorded.length > 0 && <section>
            <h3>有检测记录 · {recorded.length}</h3>
            <ul className="fingerprint-results-list">{recordedRecentFirst.map(item => <FingerprintDetailRow key={fingerprintKey(item.target)} item={item}/>)}</ul>
          </section>}
          {unrecorded.length > 0 && <section>
            <h3>无检测记录 · {unrecorded.length}</h3>
            <ul className="fingerprint-results-list">{unrecorded.map(item => <FingerprintDetailRow key={fingerprintKey(item.target)} item={item}/>)}</ul>
          </section>}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
export function FingerprintPanel({ targets, revisions, onlySupported, inline }: { targets: FingerprintTarget[]; revisions?: Record<string,string>; onlySupported?: boolean; inline?: boolean }) {
  const query = useModelFingerprints(), client = useQueryClient();
  const detailId = useId();
  const submitting = useRef(new Set<string>());
  const [attempts, setAttempts] = useState<Record<string, {busy: boolean; error?: string}>>({});
  const [expanded, setExpanded] = useState<string>();
  async function run(target: FingerprintTarget, stop = false) {
    const key = fingerprintKey(target);
    if (submitting.current.has(key)) return;
    submitting.current.add(key);
    setAttempts(current => ({...current, [key]: {busy: true}}));
    let error: string | undefined;
    try {
      await controlRequest("/v1/model-fingerprints", { method: stop ? "DELETE" : "POST", body: JSON.stringify({ targets: [target] }) });
      await client.invalidateQueries({queryKey:["model-fingerprints"]});
    } catch (e) { error = e instanceof Error ? e.message : "检测提交失败"; }
    finally {
      submitting.current.delete(key);
      setAttempts(current => ({...current, [key]: {busy: false, error}}));
    }
  }
  const visible = onlySupported ? targets.filter(t => query.supportedModels.includes(t.model) || query.index.has(fingerprintKey(t))) : targets;
  if (!visible.length) return null;
  const rows = visible.map((target, i) => {
      const key = fingerprintKey(target);
      let record = query.index.get(key);
      if (record?.state === "done" && target.source_id && fingerprintKey(record) === key && revisions && record.revision !== revisions[JSON.stringify([target.source_id,target.provider])]) record = undefined;
      const pending = fingerprintBusy(record), result = record?.result;
      const retained = record?.retained_samples || 0;
      const partial = retained > 0 && retained < 3;
      const supported = query.supportedModels.includes(target.model);
      const actionTarget = pending && record ? {source_id:record.source_id,provider:record.provider,account_id:record.account_id,group_id:record.group_id,model:record.model} : target;
      const attempt = attempts[fingerprintKey(actionTarget)] ?? attempts[key];
      const availableDetails = Boolean(result || record?.message || inline && attempt?.error);
      const open = expanded === key;
      const unavailable = !query.isError && !query.isPending && !supported;
      const label = pending ? "取消检测" : partial ? "继续采样" : inline ? "指纹检测" : "检测指纹";
      const action = <button className="button small" type="button"
        title={inline ? `${target.model} · ${fingerprintNote}` : undefined}
        disabled={attempt?.busy || query.isError || query.isPending || (!pending && !supported)}
        aria-busy={attempt?.busy} onClick={() => void run(actionTarget, pending)}>
        {inline && (attempt?.busy ? <Spinner small/> : <Play size={13}/>)} {label}
      </button>;
      const disclosure = availableDetails && <button className="button small icon-button fingerprint-disclosure" type="button" aria-label="检测详情" aria-expanded={open} aria-controls={`${detailId}-${i}`} title="检测详情" onClick={() => setExpanded(open ? undefined : key)}><ChevronDown size={14}/></button>;
      const diagnostics = availableDetails && <div className="fingerprint-diagnostics" hidden={!open} id={`${detailId}-${i}`}>
          {inline && attempt?.error && <p className="negative">{attempt.error}</p>}
          {record?.message && <p className="muted">{record.message}</p>}
          {result && <>
          {result.checked_at > 0 && <time className="muted" dateTime={new Date(result.checked_at * 1000).toISOString()}>{time(result.checked_at)}</time>}
          <p className="muted">有效样本 {result.used_outputs}/3 · 目标类别 {result.expected_class}</p>
          {result.decision === "not_model" && <p className="muted">{nonModelNote}</p>}
          {partial && <p className="muted">已保留 {retained} 个有效样本，{pending ? "正在补充" : "再次检测仅补充"}剩余 {3-retained} 个。</p>}
          {!retained && result.used_outputs > 0 && result.used_outputs < 3 && <p className="muted">此旧记录未保留可续采样本，下一次检测将开始可累计的新一轮。</p>}
          {partialFingerprint(result) ? <FingerprintCandidateRanking partial={result}/> :
            result.candidates?.length > 0 && <ol>{result.candidates.map(c => <li key={c.model}>{c.display_name} <span className="mono">{rate(c.probability)}</span></li>)}</ol>}
          {result.samples?.filter(s=>s.state==="error").map(s=><p key={s.request_id}>{fingerprintSampleFailure(s).detail}</p>)}
          {result.reference_commit && <small className="muted">lm-detector · {result.reference_commit.slice(0,12)}</small>}
          </>}
        </div>;
      if (inline) return <div className="detail-check-item detail-check-fingerprint" key={key}>
        {action}
        <span role="status" className="muted">
          {attempt?.busy ? "正在提交…" : attempt?.error ? <span className="negative">提交失败</span> :
            query.isError ? <span title={fingerprintReadMessage(query.error)}>读取失败</span> :
            query.isPending ? "读取中…" : unavailable && !record ? "不适用" : <FingerprintStatus record={record}/>}
        </span>
        {query.isError ? <button className="button small icon-button fingerprint-disclosure" type="button"
          aria-label="指纹检测读取失败 · 重试" title="重试" onClick={() => void query.refetch()}><RefreshCw size={14}/></button> : disclosure}
        {attempt?.error && <span className="sr-only" role="alert">{attempt.error}</span>}
        {diagnostics}
      </div>;
      return <div className="fingerprint-detail" key={key}>
        <div className="fingerprint-heading"><strong title={target.model}>{target.model}</strong>{query.isError ? <span className="muted">待启用</span> : unavailable && !record ? <span className="muted" title="指纹检测服务待更新">指纹检测服务待更新</span> : <FingerprintStatus record={record}/>}
          {disclosure}
          {unavailable && <button className="button small icon-button" aria-label="重新检查" title="重新检查" onClick={() => void query.refetch()}><Fingerprint size={14}/></button>}
          {action}
        </div>
        {attempt?.error && <p role="alert">{attempt.error}</p>}
        {diagnostics}
      </div>;
    });
  if (inline) return <>{rows}</>;
  return <section className="fingerprint-panel">
    <h3><Fingerprint size={16}/>模型指纹 <Tip text={fingerprintNote}><CircleHelp size={13}/></Tip></h3>
    {query.isError && <p role="alert">{fingerprintReadMessage(query.error)} <button className="button small" onClick={() => void query.refetch()}>重试</button></p>}
    {rows}
  </section>;
}
