import { useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, CircleHelp, Fingerprint, X } from "lucide-react";
import { ApiError, controlRequest } from "./api";
import { channelMembers } from "./channelManagement";
import type { ManagementRow } from "./channelManagement";
import { rate, time } from "./format";
import { Spinner, Tip } from "./ui";

export const FINGERPRINT_MODELS = ["gpt-6-astra", "gpt-6.1-sol"];
export const fingerprintLabels: Record<string, string> = {
  match: "匹配", mismatch: "不匹配", insufficient: "样本不足", error: "检测失败",
  queued: "排队中", running: "检测中", interrupted: "已中断", untested: "未检测", not_applicable: "不适用",
};
export const fingerprintNote = "每个模型发起 3 次请求。Astra 与 6.1-sol 共用一个指纹类别；概率是请求类别在参考库内的相对概率，不代表身份保证或不降智概率。";
export interface FingerprintTarget {
  source_id?: string; provider?: string; account_id?: string; group_id?: number; model: string;
}
export interface FingerprintResult {
  model: string; status: string; probability: number | null;
  prediction: string; prediction_name: string; expected_class: string;
  used_outputs: number; checked_at: number; reference_commit: string;
  candidates: { model: string; display_name: string; probability: number | null }[];
  samples?: { request_id: string; state: string; message?: string; http_status?: number }[];
}
export interface FingerprintRecord extends FingerprintTarget {state: string; message: string; revision: string; result: FingerprintResult | null}
export const fingerprintKey = (t: FingerprintTarget) => JSON.stringify([t.source_id || "", t.provider || "", t.account_id || "", t.group_id || 0, t.model]);
export const fingerprintReadMessage = (error: unknown) => error instanceof ApiError && error.status === 404 ? "指纹检测服务待更新" : "模型指纹读取失败";
export const fingerprintBusy = (record?: FingerprintRecord) => record?.state === "queued" || record?.state === "running";
export function fingerprintStatus(record?: FingerprintRecord) {
  return !record ? "untested" : record.state !== "done" ? record.state : record.result?.status || "error";
}
export function fingerprintProbabilityClass(p: number | null) {
  return p === null ? "quality-unknown" : p === 1 ? "quality-perfect" : p >= .9 ? "quality-partial" : "quality-poor";
}
export function fingerprintTargets(row: Pick<ManagementRow, "account" | "target" | "configured">, models = FINGERPRINT_MODELS): FingerprintTarget[] {
  const supported = [...new Set(models)].filter(model => FINGERPRINT_MODELS.includes(model));
  if (row.account.id) return supported.map(model => ({ account_id: row.account.id, group_id: row.target.group_id, model }));
  if (!row.configured) return [];
  const members = channelMembers(row.configured);
  return supported.map(model => {
    const member = members.find(m => m.models.includes(model)) || members[0];
    return { source_id: member.source_id, provider: member.provider, model };
  });
}
export function rowFingerprints(row: ManagementRow, index: Map<string, FingerprintRecord>, model: string) {
  return fingerprintTargets(row, model ? [model] : FINGERPRINT_MODELS).map(target => {
    let record = index.get(fingerprintKey(target));
    if (record?.state === "done" && target.source_id && row.configured) {
      const member = channelMembers(row.configured).find(m => m.source_id === target.source_id && m.provider === target.provider);
      if (member?.probe_fingerprint !== record.revision) record = undefined;
    }
    return { target, record };
  });
}
export function useModelFingerprints() {
  const query = useQuery({
    queryKey: ["model-fingerprints"],
    queryFn: ({ signal }) => controlRequest<{data: FingerprintRecord[]}>("/v1/model-fingerprints", { signal }),
    staleTime: 5000, refetchInterval: q => q.state.data?.data?.some(fingerprintBusy) ? 5000 : 15000, retry: false,
  });
  const index = useMemo(() => new Map((Array.isArray(query.data?.data) ? query.data.data : []).map(r => [fingerprintKey(r), r])), [query.data]);
  return { ...query, index };
}
export function FingerprintStatus({ record }: { record?: FingerprintRecord }) {
  const status = fingerprintStatus(record), result = record?.state === "done" ? record.result : null;
  const probability = result && (status === "match" || status === "mismatch") ? result.probability : null;
  return <div className="quality-status-stack">
    <span className={`check-status ${status === "match" ? "pass" : status === "mismatch" ? "fail" : "inconclusive"}`}>
      {fingerprintBusy(record) ? <Spinner small /> : status === "match" ? <Check size={14}/> : status === "mismatch" ? <X size={14}/> : <CircleHelp size={14}/>}
      {fingerprintLabels[status] || "检测失败"}
    </span>
    <Tip text={`请求类别匹配概率：${rate(probability)}。${fingerprintNote}${result?.prediction_name ? ` 最接近：${result.prediction_name}。` : ""}${record?.message || ""}`}>
      <small className={`quality-probability ${fingerprintProbabilityClass(probability)}`}>{rate(probability)}</small>
    </Tip>
  </div>;
}
export function FingerprintCell({ entries, error, loading }: { entries: ReturnType<typeof rowFingerprints>; error?: Error | null; loading?: boolean }) {
  if (entries.length && (error || loading)) return <span className="muted">{error ? fingerprintReadMessage(error) : "读取中…"}</span>;
  if (!entries.length) return <span className="muted">—</span>;
  return <div className="fingerprint-cell">{entries.map(({target,record}) => <div key={fingerprintKey(target)}>
    {entries.length > 1 && <small className="check-source">{target.model}</small>}
    <FingerprintStatus record={record}/>
  </div>)}</div>;
}
export function FingerprintPanel({ targets, revisions }: { targets: FingerprintTarget[]; revisions?: Record<string,string> }) {
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
  if (!targets.length) return null;
  return <section className="fingerprint-panel">
    <h3><Fingerprint size={16}/>模型指纹 <Tip text={fingerprintNote}><CircleHelp size={13}/></Tip></h3>
    {query.isError && <p role="alert">{fingerprintReadMessage(query.error)} <button className="button small" onClick={() => void query.refetch()}>重试</button></p>}
    {error && <p role="alert">{error}</p>}
    {targets.map(target => {
      let record = query.index.get(fingerprintKey(target));
      if (record?.state === "done" && target.source_id && revisions && record.revision !== revisions[JSON.stringify([target.source_id,target.provider])]) record = undefined;
      const pending = fingerprintBusy(record), result = record?.state === "done" ? record.result : null;
      return <div className="fingerprint-detail" key={fingerprintKey(target)}>
        <div className="fingerprint-heading"><strong>{target.model}</strong>{query.isError ? <span className="muted">待启用</span> : <FingerprintStatus record={record}/>}
          <button className="button small" disabled={busy || query.isError || query.isPending} title={fingerprintNote} onClick={() => void run(target, pending)}>{pending ? "取消检测" : "检测指纹"}</button>
        </div>
        {record?.message && <p className="muted">{record.message}</p>}
        {result && <details><summary>检测详情{result.checked_at ? ` · ${time(result.checked_at)}` : ""}</summary>
          <p className="muted">有效样本 {result.used_outputs}/3 · 目标类别 {result.expected_class}</p>
          {result.candidates?.length > 0 && <ol>{result.candidates.map(c => <li key={c.model}>{c.display_name} <span className="mono">{rate(c.probability)}</span></li>)}</ol>}
          {result.samples?.filter(s=>s.state==="error").map(s=><p key={s.request_id}>{s.message}{s.http_status ? ` · HTTP ${s.http_status}` : ""}</p>)}
          <small className="muted">lm-detector · {result.reference_commit?.slice(0,12)}</small>
        </details>}
      </div>;
    })}
  </section>;
}
