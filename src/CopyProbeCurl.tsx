import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { controlRequest } from "./api";
import type { Probe } from "./Sub2apiChecks";
import { Spinner } from "./ui";

export interface ProbeCurlTarget {
  account_id?: string;
  group_id?: number;
  source_id?: string;
  provider?: string;
}
export function CopyProbeCurl({ probe, kind, targets, label }: {
  probe?: Probe;
  kind: "availability" | "quality" | "tool-use" | "compaction";
  targets: ProbeCurlTarget[];
  label: string;
}) {
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const controller = useRef(new AbortController());
  useEffect(() => {
    const abort = new AbortController();
    controller.current = abort;
    setCopied(false);
    setMessage("");
    setError("");
    setBusy(false);
    return () => abort.abort();
  }, [probe?.id, probe?.curl_token, kind]);
  const available = !!(probe?.curl_token || probe?.id);
  async function copy() {
    if (busy || !available) return;
    const signal = controller.current.signal;
    setBusy(true);
    setCopied(false);
    setError("");
    setMessage("");
    try {
      const result = await controlRequest<{ curl: string; original: boolean; message?: string }>(
        "/v1/sub2api/check-curl",
        { method: "POST", signal, body: JSON.stringify(probe?.curl_token
          ? { token: probe.curl_token }
          : { probe_id: probe?.id, kind, targets }) },
      );
      signal.throwIfAborted();
      await navigator.clipboard.writeText(result.curl);
      if (!signal.aborted) {
        setCopied(true);
        setMessage(result.original ? "" : result.message || "已按当前配置生成");
      }
    } catch (e) {
      if (!signal.aborted) setError(e instanceof Error ? e.message : "复制失败，请重试");
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  }
  return <span className="probe-curl-action">
    <button type="button" className="button small" aria-label={`${label}复制为 curl`}
      title={available ? "复制渠道 URL、API key、请求头和请求体" : "尚无检测请求，请先完成检测"}
      disabled={!available || busy} onClick={() => void copy()}>
      {busy ? <Spinner small /> : copied ? <Check size={13} /> : <Copy size={13} />}
      {copied ? "已复制" : "复制为 curl"}
    </button>
    {message && <small role="status">{message}</small>}
    {error && <small role="alert">{error}</small>}
  </span>;
}
