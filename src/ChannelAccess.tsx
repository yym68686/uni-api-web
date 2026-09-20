import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Copy, Eye, EyeOff, Trash2, Check, Link2 } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { Channel } from "./types";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";

export function ChannelAccess({
  row,
  imports,
  onRemoved,
}: {
  row: Channel;
  imports?: SubImportsQuery;
  onRemoved: () => void;
}) {
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
  const installed = imports?.data?.data.find(
    (item) =>
      item.source_id === row.source_id && item.provider === row.provider,
  );
  async function read(copy: boolean) {
    if (busy) return;
    setBusy(true);
    setError("");
    setCopied(false);
    try {
      const result =
        keys ||
        (
          await controlRequest<{ api_keys: string[] }>(
            `/v1/sources/${encodeURIComponent(row.source_id!)}/channel-info?` +
              new URLSearchParams({ provider: row.provider, reveal: "true" }),
            { signal: controller.current.signal },
          )
        ).api_keys;
      if (!result.length) throw new Error("此渠道没有可显示的 API key。");
      if (copy) {
        await navigator.clipboard.writeText(result.join("\n"));
        setCopied(true);
      } else setKeys(result);
    } catch (e) {
      if (!controller.current.signal.aborted)
        setError(e instanceof Error ? e.message : "读取失败");
    } finally {
      if (!controller.current.signal.aborted) setBusy(false);
    }
  }
  return (
    <section
      className="detail-section channel-access"
      aria-label="渠道 API key"
    >
      <div className="channel-access-heading">
        <h3>渠道 API key</h3>
        <span>{keys ? `${keys.length} 个密钥` : "已隐藏"}</span>
      </div>
      <div className="channel-secret">
        <div className="channel-secret-value">
          {keys ? (
            keys.map((key, i) => <code key={i}>{key}</code>)
          ) : (
            <code aria-label="API key 已隐藏">••••••••••••••••••••••••</code>
          )}
        </div>
        <div className="channel-secret-actions">
          <button
            className="settings-icon-button"
            aria-label={keys ? "隐藏" : "显示"}
            title={keys ? "隐藏密钥" : "显示密钥"}
            disabled={busy}
            onClick={() => (keys ? setKeys(null) : void read(false))}
          >
            {busy ? (
              <Spinner small />
            ) : keys ? (
              <EyeOff size={14} />
            ) : (
              <Eye size={14} />
            )}
          </button>
          <button
            className="settings-icon-button"
            aria-label={copied ? "已复制" : "复制"}
            title={copied ? "已复制" : "复制密钥"}
            disabled={busy}
            onClick={() => void read(true)}
          >
            {copied ? <Check size={16} /> : <Copy size={16} />}
          </button>
        </div>
      </div>
      {error && (
        <p className="negative" role="alert">
          {error}
        </p>
      )}
      {installed?.kind === "configured" && (
        <div className="channel-remove">
          <h4>站点账号关联</h4>
          <p className="muted">
            {
              {
                matched: "已自动关联，使用账号查询账单与余额",
                partial: "部分 API key 尚未匹配，暂不汇总账号消费",
                ambiguous: "同一 API key 匹配到多个账号，暂不自动关联",
                unmatched: "已保存的站点账号中未找到完全匹配的 API key",
                no_account: "尚未保存此站点的 sub2api 账号",
              }[installed.binding_status || "unmatched"]
            }
          </p>
          {(installed.bound_keys || []).map((key) => (
            <p className="muted" key={`${key.account_id}:${key.remote_key_id}`}>
              {key.account_name} · Key #{key.remote_key_id}
              {key.group_id > 0 && ` · 分组 #${key.group_id}`}
            </p>
          ))}
        </div>
      )}
      {installed && installed.kind !== "configured" && (
        <RemoveChannel item={installed} onRemoved={onRemoved} />
      )}
    </section>
  );
}
function RemoveChannel({
  item,
  onRemoved,
}: {
  item: InstalledChannel;
  onRemoved: () => void;
}) {
  const client = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function remove() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest("/v1/sub2api/channels", {
        method: "PATCH",
        signal: AbortSignal.timeout(60000),
        body: JSON.stringify({
          action: "delete",
          account_id: item.account_id,
          group_id: item.group_id,
          source_id: item.source_id,
          api_key_id: item.api_key_id,
          revision: item.revision,
        }),
      });
      await Promise.all(
        [
          "sub2api-imports",
          "catalog",
          "channel-controls",
          "control-catalog",
          "sub-import-options",
          "channel-sites",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
      onRemoved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "移除失败");
      void client.invalidateQueries({ queryKey: ["sub2api-imports"] });
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="channel-remove">
      <div className="channel-binding-row">
        <span className="channel-binding-icon">
          <Link2 size={17} />
        </span>
        <div className="channel-binding-info">
          <span>调用 API key</span>
          <strong>
            {item.source_name} · Key {item.key_position}
          </strong>
          <code>{item.key_prefix}</code>
        </div>
        {!confirm && (
          <button
            className="button channel-remove-trigger"
            aria-label="从此 API key 移除"
            disabled={!item.manageable}
            onClick={() => setConfirm(true)}
          >
            <Trash2 size={15} />
            移除
          </button>
        )}
      </div>
      {confirm && (
        <div className="channel-remove-confirm" role="alert">
          <p>
            从此 API key 移除该渠道及其 {item.models.length}{" "}
            个模型？站点账号和检测记录会保留。
          </p>
          <div className="channel-access-actions">
            <button
              className="button ghost"
              disabled={busy}
              onClick={() => setConfirm(false)}
            >
              取消
            </button>
            <button
              className="button danger"
              disabled={busy}
              onClick={() => void remove()}
            >
              {busy ? "正在移除…" : "确认移除"}
            </button>
          </div>
        </div>
      )}
      {!item.manageable && <p className="muted">来源尚不支持移除渠道。</p>}
      {error && (
        <p className="negative" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
