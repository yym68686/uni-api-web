import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Copy, Eye, EyeOff, Trash2 } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { Channel } from "./types";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";

export function ChannelAccess({ row, imports, onRemoved }: { row: Channel; imports?: SubImportsQuery; onRemoved: () => void }) {
  const [keys, setKeys] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const controller = useRef(new AbortController());
  useEffect(() => {
    const abort = new AbortController();
    controller.current = abort;
    return () => abort.abort();
  }, []);
  const installed = imports?.data?.data.find((item) => item.source_id === row.source_id && item.provider === row.provider);
  async function read(copy: boolean) {
    if (busy) return;
    setBusy(true); setError(""); setCopied(false);
    try {
      const result = keys || (await controlRequest<{ api_keys: string[] }>(`/v1/sources/${encodeURIComponent(row.source_id!)}/channel-info?` + new URLSearchParams({ provider: row.provider, reveal: "true" }), { signal: controller.current.signal })).api_keys;
      if (!result.length) throw new Error("此渠道没有可显示的 API key。");
      if (copy) { await navigator.clipboard.writeText(result.join("\n")); setCopied(true); }
      else setKeys(result);
    } catch (e) { if (!controller.current.signal.aborted) setError(e instanceof Error ? e.message : "读取失败"); }
    finally { if (!controller.current.signal.aborted) setBusy(false); }
  }
  return <section className="detail-section channel-access" aria-label="渠道 API key">
    <h3>渠道 API key</h3>
    <div className="channel-secret">{keys ? keys.map((key, i) => <code key={i}>{key}</code>) : <code aria-label="API key 已隐藏">••••••••••••••••••••••••</code>}</div>
    <div className="channel-access-actions">
      <button className="button small" disabled={busy} onClick={() => keys ? setKeys(null) : void read(false)}>{busy ? <Spinner small /> : keys ? <EyeOff size={14} /> : <Eye size={14} />}{keys ? "隐藏" : "显示"}</button>
      <button className="button small" disabled={busy} onClick={() => void read(true)}><Copy size={14} />{copied ? "已复制" : "复制"}</button>
    </div>
    {error && <p className="negative" role="alert">{error}</p>}
    {installed && <RemoveChannel item={installed} onRemoved={onRemoved} />}
  </section>;
}
function RemoveChannel({ item, onRemoved }: { item: InstalledChannel; onRemoved: () => void }) {
  const client = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function remove() {
    if (busy) return;
    setBusy(true); setError("");
    try {
      await controlRequest("/v1/sub2api/channels", { method: "PATCH", signal: AbortSignal.timeout(60000), body: JSON.stringify({ action: "delete", account_id: item.account_id, group_id: item.group_id, source_id: item.source_id, api_key_id: item.api_key_id, revision: item.revision }) });
      await Promise.all(["sub2api-imports", "catalog", "channel-controls", "control-catalog", "sub-import-options", "channel-sites"].map((name) => client.invalidateQueries({ queryKey: [name] })));
      onRemoved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "移除失败");
      void client.invalidateQueries({ queryKey: ["sub2api-imports"] });
    } finally { setBusy(false); }
  }
  return <div className="channel-remove">
    <p className="muted">已绑定：{item.source_name} · API key #{item.key_position} <span className="mono">{item.key_prefix}</span></p>
    {confirm ? <div className="sub-remove" role="alert">
      <p>从此 API key 移除该渠道及其 {item.models.length} 个模型？站点账号和检测记录会保留。</p>
      <div className="channel-access-actions"><button className="button small" disabled={busy} onClick={() => void remove()}>{busy ? "正在移除…" : "确认移除"}</button><button className="button small" disabled={busy} onClick={() => setConfirm(false)}>取消</button></div>
    </div> : <button className="button small" disabled={!item.manageable} onClick={() => setConfirm(true)}><Trash2 size={14} />从此 API key 移除</button>}
    {!item.manageable && <p className="muted">来源尚不支持移除渠道。</p>}
    {error && <p className="negative" role="alert">{error}</p>}
  </div>;
}
