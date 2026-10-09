import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";

interface RepairPreview {
  revision: string;
  plan_hash: string;
  changes: {
    provider: string;
    before_engine: string;
    before_url: string;
    after: {
      provider: string;
      engine: string;
      base_url: string;
      models: Record<string, string>;
    }[];
  }[];
  skipped: { provider: string; reason: string }[];
}

export function ChannelProtocolRepair({
  source,
  provider,
  onApplied,
}: {
  source: string;
  provider: string;
  onApplied: () => void;
}) {
  const cache = useQueryClient();
  const [preview, setPreview] = useState<RepairPreview>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [receipt, setReceipt] = useState<{
    operation_id: string;
    revision: string;
  }>();
  async function run(action: "preview" | "apply" | "rollback" | "status") {
    setBusy(true);
    setMessage("");
    const operationId =
      action === "apply" ? crypto.randomUUID() : receipt?.operation_id;
    try {
      const result = await controlRequest<
        RepairPreview & {
          status?: string;
          operation_id: string;
          message?: string;
        }
      >(`/v1/sources/${encodeURIComponent(source)}/channel-protocol-repair`, {
        method: "POST",
        signal: AbortSignal.timeout(100000),
        body: JSON.stringify({
          action,
          providers: provider ? [provider] : [],
          ...(action === "preview"
            ? {}
            : {
                revision:
                  action === "status"
                    ? undefined
                    : action === "rollback"
                      ? receipt?.revision
                      : preview?.revision,
                plan_hash: preview?.plan_hash,
                operation_id: operationId,
              }),
        }),
      });
      if (action === "preview") {
        setPreview(result);
        if (!result.changes.length)
          setMessage(
            "未发现可自动修复的旧导入配置；人工覆盖和非站点导入渠道不会自动修改。",
          );
      } else {
        setPreview(undefined);
        setReceipt(
          action === "rollback"
            ? undefined
            : result.status === "applied"
              ? result
              : receipt,
        );
        setMessage(
          result.status === "unchanged"
            ? "配置未改变。"
            : result.status === "pending"
              ? result.message || "结果待核对。"
              : action === "rollback"
                ? "已恢复修复前配置。"
                : "修复已应用并保留恢复快照。",
        );
        for (const key of [
          "sub2api-imports",
          "channel-management",
          "channel-routes",
          "catalog",
          "channel-controls",
          "channel-settings-audit",
        ])
          void cache.invalidateQueries({ queryKey: [key] });
        onApplied();
      }
    } catch (error) {
      if (action === "apply" && operationId) {
        setReceipt({ operation_id: operationId, revision: "" });
        setPreview(undefined);
      }
      setMessage(
        error instanceof Error
          ? error.message
          : "修复失败；请重新读取状态后核对。",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section
      className="settings-feedback channel-protocol-repair"
      aria-label="旧渠道协议修复"
    >
      <button
        type="button"
        className="button small"
        disabled={busy}
        onClick={() => void run("preview")}
      >
        预览旧渠道协议修复
      </button>
      {preview?.changes.map((change) => (
        <div key={change.provider}>
          <p>
            {change.provider} · {change.before_engine} · {change.before_url}
          </p>
          {change.after.map((channel) => (
            <p key={channel.provider}>
              {channel.provider} · {channel.engine} · {channel.base_url} ·{" "}
              {Object.keys(channel.models).join("、")}
            </p>
          ))}
        </div>
      ))}
      {preview?.skipped.map((item) => (
        <p key={item.provider}>{item.reason}</p>
      ))}
      {!!preview?.changes.length && (
        <button
          type="button"
          className="button"
          disabled={busy}
          onClick={() => void run("apply")}
        >
          确认修复（保留回滚快照）
        </button>
      )}
      {receipt && (
        <>
          <button
            type="button"
            className="button small"
            disabled={busy}
            onClick={() => void run("status")}
          >
            核对修复状态
          </button>
          <button
            type="button"
            className="button"
            disabled={busy || !receipt.revision}
            onClick={() => void run("rollback")}
          >
            回滚本次协议修复
          </button>
        </>
      )}
      {message && <p role="status">{message}</p>}
    </section>
  );
}
