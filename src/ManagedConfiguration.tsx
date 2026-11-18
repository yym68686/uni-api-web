import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { parse } from "yaml";
import { controlRequest } from "./api";

interface Status {
  published_revision: number;
  draft_revision: number;
  applied_revision?: number;
  requested: boolean;
  failure?: string;
}

export function ManagedConfiguration({ source }: { source: string }) {
  const [open, setOpen] = useState(false);
  const [document, setDocument] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `/v1/sources/${encodeURIComponent(source)}/managed-config`;
  const query = useQuery({
    queryKey: ["managed-configuration", source],
    queryFn: ({ signal }) => controlRequest<Status>(path, { signal }),
    enabled: open,
    refetchInterval: open ? 5000 : false,
    refetchIntervalInBackground: false,
    retry: false,
  });
  const status = query.data;

  async function act(action: "import" | "draft" | "publish") {
    if (busy || !status) return;
    setBusy(true);
    setError("");
    try {
      let bundle: unknown;
      if (action === "draft") {
        let parsed: unknown;
        try {
          parsed = parse(document);
        } catch {
          throw new Error("无法解析 YAML 或 JSON；请核对格式。");
        }
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
          throw new Error("配置必须是一个 YAML 或 JSON 对象。");
        }
        bundle = "schema" in parsed && "base" in parsed && "controls" in parsed
          ? parsed
          : { schema: 1, base: parsed, controls: { version: 2, rules: [], temporary_channels: [] } };
      }
      await controlRequest<Status>(path, {
        method: "POST",
        body: JSON.stringify({
          action,
          expected_revision: status.published_revision,
          draft_revision: status.draft_revision,
          bundle,
        }),
      });
      if (action === "draft") setDocument("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "无法保存配置");
    } finally {
      await query.refetch();
      setBusy(false);
    }
  }

  if (!open) return <button className="button small" onClick={() => setOpen(true)}>完整配置托管</button>;
  return (
    <div className="control-persistence">
      <strong>完整配置托管</strong>
      <small>先保存草稿，再交给启用了托管模式的网关校验。校验失败不会替换已发布配置。</small>
      {status && <small role="status">
        {status.published_revision ? `已发布 v${status.published_revision}` : "尚未发布"}
        {status.applied_revision ? ` · 线上已应用 v${status.applied_revision}` : " · 未确认线上已应用"}
        {status.draft_revision > 0 && ` · 草稿 v${status.draft_revision}`}
        {status.requested && " · 等待网关校验"}
      </small>}
      <label>
        新建或替换草稿（完整 YAML / JSON）
        <textarea
          value={document}
          onChange={(event) => setDocument(event.target.value)}
          autoComplete="off"
          spellCheck={false}
          rows={6}
          placeholder="可粘贴基础配置，或带 base 和 controls 的完整配置"
        />
      </label>
      <div className="recharge-actions">
        <button className="button small" disabled={!status || status.requested || busy || !document.trim()} onClick={() => void act("draft")}>保存草稿</button>
        <button className="button small" disabled={!status || status.requested || busy} onClick={() => void act("import")}>从在线网关导入草稿</button>
        <button className="button small" disabled={!status || status.draft_revision <= status.published_revision || status.requested || busy} onClick={() => void act("publish")}>校验并发布</button>
        <button className="button small ghost" disabled={busy} onClick={() => { setDocument(""); setError(""); setOpen(false); }}>收起</button>
      </div>
      {(error || query.error || status?.failure) && <small className="negative" role="alert">
        {error || query.error?.message || status?.failure}
      </small>}
    </div>
  );
}
