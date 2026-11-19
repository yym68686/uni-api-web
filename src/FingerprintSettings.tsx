import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import "./fingerprintSettings.css";

type Settings = {
  supported_models: string[];
  enabled_models: string[];
  reference_commit: string;
};

const path = "/v1/model-fingerprints/settings";
const queryKey = ["fingerprint-settings"];

export function FingerprintSettings() {
  const client = useQueryClient();
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => controlRequest<Settings>(path, { signal }),
    refetchInterval: 60_000,
    retry: false,
  });
  const [draft, setDraft] = useState<Set<string> | null>(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const settings = query.data;
  const enabled = draft ?? new Set(settings?.enabled_models);
  const selection = settings?.supported_models.filter((model) => enabled.has(model)) ?? [];
  const changed = !!settings && (selection.length !== settings.enabled_models.length ||
    selection.some((model) => !settings.enabled_models.includes(model)));

  async function save() {
    if (!changed || saving || query.isError) return;
    setSaving(true);
    setError("");
    setMessage("");
    try {
      await client.cancelQueries({ queryKey });
      const saved = await controlRequest<Settings>(path, {
        method: "PUT",
        body: JSON.stringify({ enabled_models: selection }),
      });
      client.setQueryData(queryKey, saved);
      setDraft(null);
      setMessage("已保存自动指纹检测设置");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败，勾选项未清除");
    } finally {
      setSaving(false);
    }
  }

  return <section className="fingerprint-settings" aria-labelledby="fingerprint-settings-title">
    <h3 id="fingerprint-settings-title">自动指纹检测模型</h3>
    <p className="muted">勾选并保存后，下次可用性检测成功时自动检测该模型的指纹；保存不会立即补跑。不影响手动指纹检测、可用性或降智检测。</p>
    <p className="muted">服务会从 lm-detector 定期更新已验证的支持列表。新增模型默认不勾选，更新目录不会发起检测。</p>
    {query.isPending && <p role="status">正在读取支持列表…</p>}
    {query.isError && <p role="alert" className="error-banner">指纹设置读取失败，已保存设置未改变。{query.error.message}</p>}
    {settings && <fieldset className="fingerprint-settings-models" disabled={saving || query.isError}>
      <legend>支持模型 · 已选 {selection.length}/{settings.supported_models.length}</legend>
      {settings.supported_models.map((model) => <label key={model}>
        <input type="checkbox" checked={enabled.has(model)} onChange={(event) => {
          const next = new Set(enabled);
          if (event.target.checked) next.add(model);
          else next.delete(model);
          setDraft(next);
          setMessage("");
          setError("");
        }} />
        <span>{model}</span>
      </label>)}
    </fieldset>}
    {error && <p role="alert" className="error-banner">{error}</p>}
    {message && <p role="status">{message}</p>}
    <div className="fingerprint-settings-actions">
      <button className="button small primary" disabled={!changed || saving || query.isError} onClick={() => void save()}>{saving ? "保存中…" : "保存勾选"}</button>
      <button className="button small" disabled={saving || query.isFetching} onClick={() => void query.refetch()}>重新读取列表</button>
      {settings && <small className="muted">参考目录 · <span className="mono">{settings.reference_commit.slice(0, 12)}</span></small>}
    </div>
  </section>;
}
