import { useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Check, ChevronDown, CircleHelp, Fingerprint, X } from "lucide-react";
import { ApiError, controlRequest } from "./api";
import { channelMembers, useChannelManagement } from "./channelManagement";
import type { ManagementRow } from "./channelManagement";
import { CompactDialogHeader } from "./CompactDialog";
import type { Channel } from "./types";
import { dateTime, rate, time } from "./format";
import { useSubImports } from "./sub2apiImports";
import { Spinner, Tip } from "./ui";
import { fingerprintKey, sharedFingerprintIndex } from "./fingerprintResults";
import { fingerprintFailures, fingerprintSampleFailure, observedGroupRejections } from "./fingerprintRejections";
export { fingerprintKey } from "./fingerprintResults";

const legacyFingerprintModels = ["gpt-6-astra", "gpt-6.1-sol"];
export const FINGERPRINT_BATCH_LIMIT = 512;
export const fingerprintLabels: Record<string, string> = {
  match: "匹配", mismatch: "不匹配", insufficient: "样本不足", error: "检测失败",
  queued: "排队中", running: "检测中", interrupted: "已中断", untested: "未检测", not_applicable: "不适用",
};
export const fingerprintNote = "每轮累计 3 个有效样本；1、2 个样本时显示暂定排名，满 3 个后显示匹配结果与概率。不足 3 个时保留已有样本，再次检测只补缺少的样本。满 3 个后再次检测开启新一轮；渠道配置或参考库变化时重新采样。Astra 与 6.1-sol 共用一个指纹类别；Gemini 3.1 Pro 对应 3.1 Pro Preview，3.8 Flash、4 Argon 和 4 Carbon 各自独立。DeepSeek V4.1 Flash 1017 使用独立类别及 low 推理强度，不与未标注版本的 Flash 共用身份。概率是请求类别在参考库内的相对概率，不代表身份保证或不降智概率。";
const partialNote = "当前仅显示暂定排名，满 3 个有效样本后计算匹配结果与概率。";
const nonModelNote = "数字序列特征不符合参考库要求，无法判定模型身份；未生成排名或匹配概率。";
export interface FingerprintTarget {
  source_id?: string; provider?: string; account_id?: string; group_id?: number; model: string;
}
export interface FingerprintResult {
  model: string; status: string; probability: number | null; decision?: "partial" | "scored" | "unscorable" | "not_model";
  prediction: string; prediction_name: string; expected_class: string;
  used_outputs: number; checked_at: number; reference_commit: string;
  candidates: { model: string; display_name: string; probability: number | null }[];
  samples?: { request_id: string; state: string; message?: string; http_status?: number; error_code?: string }[];
}
export interface FingerprintRecord extends FingerprintTarget {state: string; message: string; revision: string; updated_at_ms?: number; retained_samples?: number; result: FingerprintResult | null}
export const fingerprintReadMessage = (error: unknown) => error instanceof ApiError && error.status === 404 ? "指纹检测服务待更新" : "模型指纹读取失败";
export const fingerprintBusy = (record?: FingerprintRecord) => record?.state === "queued" || record?.state === "running";
export function fingerprintStatus(record?: FingerprintRecord) {
  return !record ? "untested" : record.state !== "done" ? record.state : record.result?.status || "error";
}
function partialFingerprint(result?: FingerprintResult | null) {
  return result?.decision === "partial" && result.used_outputs > 0 && result.used_outputs < 3 && result.candidates?.length > 0;
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
export function FingerprintStatus({ record }: { record?: FingerprintRecord }) {
  const status = fingerprintStatus(record), partial = partialFingerprint(record?.result);
  const result = record?.state === "done" || partial ? record?.result : null;
  const probability = result && (status === "match" || status === "mismatch") ? result.probability : null;
  const failure = [...new Set(fingerprintFailures(result).map(item => item.detail))].join("；");
  return <div className="quality-status-stack">
    <span className={`check-status ${status === "match" ? "pass" : status === "mismatch" ? "fail" : "inconclusive"}`}>
      {fingerprintBusy(record) ? <Spinner small /> : status === "match" ? <Check size={14}/> : status === "mismatch" ? <X size={14}/> : <CircleHelp size={14}/>}
      {fingerprintLabel(status, result)}
    </span>
    {partial && result ? <Tip text={`${partialNote}${failure}${record?.message || ""}`}>
      <small className="muted">暂定排名 · {result.used_outputs}/3<br/>最接近：{result.prediction_name}</small>
    </Tip> : <Tip text={failure || (result?.decision === "not_model" ? nonModelNote : `请求类别匹配概率：${rate(probability)}。${fingerprintNote}${result?.prediction_name ? ` 最接近：${result.prediction_name}。` : ""}${record?.message || ""}`)}>
      <small className={`quality-probability ${fingerprintProbabilityClass(probability)}`}>{rate(probability)}</small>
    </Tip>}
  </div>;
}
export function FingerprintCell({ entries, error, loading, supportedModels = [], channel }: { entries: readonly {target: FingerprintTarget; record?: FingerprintRecord}[]; error?: Error | null; loading?: boolean; supportedModels?: string[]; channel?: string }) {
  if (entries.length && error) return <Tip text={fingerprintReadMessage(error)}><span className="muted">读取失败</span></Tip>;
  if (entries.length && loading) return <span className="muted">读取中…</span>;
  if (!entries.length) return <span className="muted">—</span>;
  const summaries = entries.map(({target, record}) => {
    const status = fingerprintStatus(record);
    const partial = partialFingerprint(record?.result);
    const result = record?.state === "done" || partial ? record?.result : null;
    const probability = result && (status === "match" || status === "mismatch") ? result.probability : null;
    const unsupported = !record && !supportedModels.includes(target.model);
    const label = unsupported ? "待更新" : fingerprintLabel(status, result);
    const updatedAt = record?.updated_at_ms ?? (result?.checked_at || 0) * 1000;
    return {target, status, label, probability, partial, result, updatedAt, recorded: !!record, message:unsupported ? "指纹检测服务待更新" : record?.message};
  });
  const recorded = summaries.filter(item => item.recorded);
  // The all-model view includes untested candidates; those aren't fingerprint results.
  const visible = recorded.length ? recorded : summaries;
  const [first] = visible;
  const mismatch = visible.some(item => item.status === "mismatch");
  const label = visible.every(item => item.label === first.label) ? first.label : mismatch ? "有不匹配" : "多种状态";
  const probability = visible.length === 1 ? first.probability : null;
  const summary = <>
    <span className={label === "匹配" ? "check-status pass" : mismatch ? "check-status fail" : "muted"}>{label}</span>
    {probability != null && <small className={`quality-probability ${fingerprintProbabilityClass(probability)}`}>{rate(probability)}</small>}
    {recorded.length > 1 && <small className="muted" aria-label={`${recorded.length} 个模型有检测记录`}>{recorded.length}</small>}
  </>;
  const detail = ({target, label, probability, partial, result, message}: typeof first) => <>
      <strong>{target.model} · {label}</strong>
      {partial && result && <div>暂定排名 · {result.used_outputs}/3</div>}
      {probability != null && <div>匹配概率：{rate(probability)}</div>}
      {result?.prediction_name && <div>最接近：{result.prediction_name}</div>}
      {message && <div>{message}</div>}
      {[...new Set(fingerprintFailures(result).map(item => item.detail))].map(detail => <div key={detail}>{detail}</div>)}
  </>;
  if (summaries.length === 1) return <Tip text={<div>{detail(first)}</div>}>
    <span className="fingerprint-cell">{summary}</span>
  </Tip>;
  const unrecorded = summaries.filter(item => !item.recorded);
  const recordedRecentFirst = recorded.sort((a, b) =>
    b.updatedAt - a.updatedAt ||
    a.target.model.localeCompare(b.target.model),
  );
  return <Dialog.Root>
    <Dialog.Trigger type="button" className="fingerprint-cell fingerprint-details-trigger">
      {summary}<ChevronDown size={13} aria-hidden="true" /><span className="sr-only">查看{channel ? ` ${channel} 的` : ""}模型指纹</span>
    </Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" onClick={event => event.stopPropagation()} />
      <Dialog.Content className="guide-dialog compact-dialog fingerprint-details-dialog" onClick={event => event.stopPropagation()}>
        <CompactDialogHeader context={channel || `共 ${summaries.length} 个模型`}>模型指纹详情</CompactDialogHeader>
        <div className="compact-dialog-scroll fingerprint-details-scroll" tabIndex={0} role="region" aria-label="模型指纹列表">
          {recorded.length > 0 && <section>
            <h3>有检测记录 · {recorded.length}</h3>
            <ul className="dialog-preview-list fingerprint-details-grid">{recordedRecentFirst.map(item => <li key={fingerprintKey(item.target)}>
              {detail(item)}
              {!!item.result?.checked_at && <small>指纹检测时间：{dateTime(item.result.checked_at)}</small>}
            </li>)}</ul>
          </section>}
          {unrecorded.length > 0 && <section>
            <h3>无检测记录 · {unrecorded.length}</h3>
            <ul className="dialog-preview-list fingerprint-details-grid">{unrecorded.map(item => <li key={fingerprintKey(item.target)}>{detail(item)}</li>)}</ul>
          </section>}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
export function FingerprintPanel({ targets, revisions, onlySupported }: { targets: FingerprintTarget[]; revisions?: Record<string,string>; onlySupported?: boolean }) {
  const query = useModelFingerprints(), client = useQueryClient();
  const submitting = useRef(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  async function run(target: FingerprintTarget, stop = false) {
    if (submitting.current) return;
    submitting.current = true; setBusy(true); setError("");
    try {
      await controlRequest("/v1/model-fingerprints", { method: stop ? "DELETE" : "POST", body: JSON.stringify({ targets: [target] }) });
      await client.invalidateQueries({queryKey:["model-fingerprints"]});
    } catch (e) { setError(e instanceof Error ? e.message : "检测提交失败"); }
    finally { submitting.current = false;setBusy(false); }
  }
  const visible = onlySupported ? targets.filter(t => query.supportedModels.includes(t.model) || query.index.has(fingerprintKey(t))) : targets;
  if (!visible.length) return null;
  return <section className="fingerprint-panel">
    <h3><Fingerprint size={16}/>模型指纹 <Tip text={fingerprintNote}><CircleHelp size={13}/></Tip></h3>
    {query.isError && <p role="alert">{fingerprintReadMessage(query.error)} <button className="button small" onClick={() => void query.refetch()}>重试</button></p>}
    {error && <p role="alert">{error}</p>}
    {visible.map(target => {
      let record = query.index.get(fingerprintKey(target));
      if (record?.state === "done" && target.source_id && fingerprintKey(record) === fingerprintKey(target) && revisions && record.revision !== revisions[JSON.stringify([target.source_id,target.provider])]) record = undefined;
      const pending = fingerprintBusy(record), result = record?.result;
      const retained = record?.retained_samples || 0;
      const partial = retained > 0 && retained < 3;
      const supported = query.supportedModels.includes(target.model);
      const actionTarget = pending && record ? {source_id:record.source_id,provider:record.provider,account_id:record.account_id,group_id:record.group_id,model:record.model} : target;
      return <div className="fingerprint-detail" key={fingerprintKey(target)}>
        <div className="fingerprint-heading"><strong>{target.model}</strong>{query.isError ? <span className="muted">待启用</span> : !supported && !record ? <span className="muted">指纹检测服务待更新</span> : <FingerprintStatus record={record}/>}
          <button className="button small" disabled={busy || query.isError || query.isPending || (!pending && !supported)} title={fingerprintNote} onClick={() => void run(actionTarget, pending)}>{pending ? "取消检测" : partial ? "继续采样" : "检测指纹"}</button>
        </div>
        {!query.isError && !query.isPending && !supported && <p className="muted">当前指纹参考库未启用此模型。<button className="button small" onClick={() => void query.refetch()}>重新检查</button></p>}
        {record?.message && <p className="muted">{record.message}</p>}
        {result && <details><summary>检测详情{result.checked_at ? ` · ${time(result.checked_at)}` : ""}</summary>
          <p className="muted">有效样本 {result.used_outputs}/3 · 目标类别 {result.expected_class}</p>
          {partialFingerprint(result) && <p className="muted">{partialNote}</p>}
          {result.decision === "not_model" && <p className="muted">{nonModelNote}</p>}
          {partial && <p className="muted">已保留 {retained} 个有效样本，{pending ? "正在补充" : "再次检测仅补充"}剩余 {3-retained} 个。</p>}
          {!retained && result.used_outputs > 0 && result.used_outputs < 3 && <p className="muted">此旧记录未保留可续采样本，下一次检测将开始可累计的新一轮。</p>}
          {result.candidates?.length > 0 && <ol>{result.candidates.map(c => <li key={c.model}>{c.display_name}{!partialFingerprint(result) && <> <span className="mono">{rate(c.probability)}</span></>}</li>)}</ol>}
          {result.samples?.filter(s=>s.state==="error").map(s=><p key={s.request_id}>{fingerprintSampleFailure(s).detail}</p>)}
          <small className="muted">lm-detector · {result.reference_commit?.slice(0,12)}</small>
        </details>}
      </div>;
    })}
  </section>;
}
