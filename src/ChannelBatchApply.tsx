import { useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { Layers, Trash2, X } from "lucide-react";
import { Spinner } from "./ui";
import {
  prepareChannelBatch,
  applyChannelBatch,
  batchPartLabels,
} from "./channelBatch";
import type { BatchDraft, BatchPlan, BatchProgress } from "./channelBatch";

export function ChannelBatchApply({
  draft,
  disabled,
  onApplied,
  section,
  remove = false,
  onPreview,
}: {
  draft: () => BatchDraft;
  disabled?: boolean;
  onApplied: () => void;
  section?: string;
  remove?: boolean;
  onPreview?: () => void;
}) {
  const [snapshot, setSnapshot] = useState<BatchDraft | null>(null);
  const [busy, setBusy] = useState(false);
  return (
    <Dialog.Root
      open={!!snapshot}
      onOpenChange={(open) => {
        if (busy) return;
        if (open) onPreview?.();
        setSnapshot(open ? structuredClone(draft()) : null);
      }}
    >
      <Dialog.Trigger asChild>
        <button
          type="button"
          className={`button batch-apply-trigger${section || remove ? " small" : ""}${remove ? " batch-remove-trigger" : ""}`}
          aria-label={section ? `将${section}应用于所有已保存渠道` : undefined}
          disabled={disabled}
        >
          {remove ? <Trash2 size={15} /> : <Layers size={15} />}
          {remove
            ? "删除所有已保存接入"
            : section
              ? "应用此项于所有"
              : "应用全部于所有已保存渠道"}
        </button>
      </Dialog.Trigger>
      {snapshot && (
        <BatchDialog
          draft={snapshot}
          busy={busy}
          setBusy={setBusy}
          close={() => setSnapshot(null)}
          onApplied={onApplied}
        />
      )}
    </Dialog.Root>
  );
}

function BatchDialog({
  draft,
  busy,
  setBusy,
  close,
  onApplied,
}: {
  draft: BatchDraft;
  busy: boolean;
  setBusy: (b: boolean) => void;
  close: () => void;
  onApplied: () => void;
}) {
  const client = useQueryClient();
  const deleting = draft.part === "delete",
    verb = deleting ? "删除" : "应用";
  const [plan, setPlan] = useState<BatchPlan | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);
  const [progress, setProgress] = useState<Record<string, BatchProgress>>({});
  const [finished, setFinished] = useState(false);
  const [stopping, setStopping] = useState(false);
  const stop = useRef(false);
  // Navigating away must stop later writes; an in-flight write is not aborted
  // because it may already have reached the gateway.
  useEffect(
    () => () => {
      stop.current = true;
    },
    [],
  );
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setPlan(null);
    void prepareChannelBatch(draft, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setPlan(value);
      })
      .catch((e) => {
        if (!controller.signal.aborted)
          setError(e instanceof Error ? e.message : "无法读取全部接入");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [draft, reload]);
  const done = Object.values(progress).filter((p) => p.state === "done").length;
  const failed = Object.values(progress).some((p) => p.state === "error");
  const skipped = plan?.targets.filter((t) => t.skip).length || 0;
  async function apply() {
    if (!plan || busy || finished) return;
    setBusy(true);
    stop.current = false;
    try {
      await applyChannelBatch(
        plan,
        (p) => setProgress((old) => ({ ...old, [p.id]: p })),
        () => stop.current,
      );
      setFinished(true);
    } finally {
      await Promise.all(
        [
          "channel-management",
          "channel-routes",
          "sub2api-imports",
          "catalog",
          "control-catalog",
          "channel-controls",
          "sub-import-options",
          "configured-import-options",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
      setBusy(false);
    }
  }
  function finish() {
    close();
    if (done) onApplied();
  }
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay batch-apply-overlay" />
      <Dialog.Content
        className="guide-dialog route-workspace batch-apply-dialog"
        onEscapeKeyDown={(e) => {
          e.preventDefault();
          if (!busy) finish();
        }}
        onPointerDownOutside={(e) => e.preventDefault()}
      >
        <header className="route-dialog-header">
          <Dialog.Title>
            {deleting
              ? batchPartLabels.delete
              : `${batchPartLabels[draft.part]} · 应用于所有已保存渠道`}
          </Dialog.Title>
          <Dialog.Description>
            {draft.name} · 跨来源{deleting ? "移除" : "更新"}已有 API key 接入
          </Dialog.Description>
          <button
            type="button"
            className="icon-button detail-close"
            aria-label="关闭批量应用"
            disabled={busy}
            onClick={finish}
          >
            <X size={18} />
          </button>
        </header>
        <div className="route-dialog-body">
          {loading && (
            <p role="status">
              <Spinner small /> 正在读取全部来源和已保存接入…
            </p>
          )}
          {error && (
            <div role="alert" className="error-banner">
              {error}
              <button
                type="button"
                className="button small"
                onClick={() => setReload((n) => n + 1)}
              >
                重新读取
              </button>
            </div>
          )}
          {plan && (
            <>
              <p className="batch-apply-summary">
                将{deleting ? "删除" : "更新"}{" "}
                {new Set(plan.targets.map((t) => t.source)).size} 个来源、
                {
                  new Set(
                    plan.targets.map((t) => JSON.stringify([t.source, t.key])),
                  ).size
                }{" "}
                个 API key，共 {plan.targets.length} 个渠道接入。
              </p>
              {deleting ? (
                <p className="sub-import-note negative">
                  确认后，下列 API key
                  将无法再通过此渠道调用模型；这些接入的全部模型路由都会移除。基础渠道定义和其他渠道保留。
                </p>
              ) : (
                <>
                  <p className="sub-import-note">
                    {draft.part === "all"
                      ? "以当前编辑内容替换模型勾选、重命名和路由位置。"
                      : draft.part === "models"
                        ? "仅同步原模型的勾选，保留各 Key 的重命名及已有模型位置。新增模型放到第 1 位。"
                        : draft.part === "aliases"
                          ? "仅同步模型重命名，保留各 Key 的原模型勾选及已有模型位置。新增别名放到第 1 位；空列表表示清除已有重命名。"
                          : "仅同步下面模型的路由位置，各 Key 的模型勾选和重命名保持不变。没有对应模型的接入会跳过。"}{" "}
                    只更新已有接入。超出目标路由数量的位置放到末位；同一 Key
                    有多个相关渠道时依次插入，最终位置见下方。
                  </p>
                  <div className="batch-draft-models" aria-label="将应用的模型">
                    {Object.entries(
                      draft.part === "models"
                        ? draft.originals
                        : draft.part === "aliases"
                          ? draft.aliases
                          : draft.models,
                    ).map(([name, up]) => (
                      <span key={name}>
                        {up === name ? name : `${up} → ${name}`}
                        {(draft.part === "all" ||
                          draft.part === "positions") && (
                          <small>第 {draft.positions[name] || 1} 位</small>
                        )}
                      </span>
                    ))}
                  </div>
                </>
              )}
              <div className="batch-target-table">
                <table aria-label="批量应用范围">
                  <thead>
                    <tr>
                      <th>来源 / API key</th>
                      <th>渠道</th>
                      <th>变更与位置</th>
                      <th>状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    {plan.targets.map((t) => {
                      const p = progress[t.id],
                        removed = Object.keys(t.current).filter(
                          (m) => !(m in t.models),
                        );
                      return (
                        <tr key={t.id}>
                          <td>
                            {t.sourceName}
                            <small>Key {t.keyPosition}</small>
                          </td>
                          <td>{t.name}</td>
                          <td>
                            <span>
                              {deleting
                                ? `移除全部 ${Object.keys(t.current).length} 个模型`
                                : `${Object.keys(t.current).length} → ${Object.keys(t.models).length} 个模型`}
                            </span>
                            <details>
                              <summary>
                                {deleting
                                  ? "查看将移除的模型"
                                  : t.clamped
                                    ? "部分位置调整为末位 · 查看详情"
                                    : "查看模型位置"}
                              </summary>
                              {Object.entries(
                                t.finalPositions || t.positions,
                              ).map(([model, pos]) => (
                                <div key={model}>
                                  {t.models[model] === model
                                    ? model
                                    : `${t.models[model]} → ${model}`}{" "}
                                  · 第 {pos} 位
                                </div>
                              ))}
                              {!!removed.length && (
                                <p className="negative">
                                  移除：{removed.join("、")}
                                </p>
                              )}
                            </details>
                          </td>
                          <td
                            className={
                              p?.state === "error"
                                ? "negative"
                                : p?.state === "done"
                                  ? "positive"
                                  : ""
                            }
                          >
                            {t.skip ||
                              (p?.state === "done" ? (
                                `已${verb}`
                              ) : p?.state === "error" ? (
                                "未确认"
                              ) : p?.state === "running" && busy ? (
                                <>
                                  <Spinner small />
                                  {verb}中
                                </>
                              ) : finished ? (
                                "未执行"
                              ) : (
                                `待${verb}`
                              ))}
                            {p?.message && <small>{p.message}</small>}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              {(busy || finished) && (
                <p role="status" className="batch-apply-summary">
                  已确认{verb} {done}/{plan.targets.length - skipped} 个接入。
                  {skipped > 0 && `跳过 ${skipped} 个无对应模型的接入。`}
                  {failed
                    ? "遇到错误，已停止后续操作。请核对未确认项后重新打开预览。"
                    : finished && done + skipped < plan.targets.length
                      ? "已停止，尚未执行的接入保持原配置。"
                      : finished
                        ? `全部${verb}完成。`
                        : ""}
                </p>
              )}
            </>
          )}
        </div>
        <footer className="batch-apply-actions">
          {busy ? (
            <button
              type="button"
              className="button"
              disabled={stopping}
              onClick={() => {
                stop.current = true;
                setStopping(true);
              }}
            >
              {" "}
              {stopping ? "正在等待当前请求结束…" : `停止后续${verb}`}
            </button>
          ) : (
            <>
              <button type="button" className="button" onClick={finish}>
                {finished ? "完成" : "取消"}
              </button>
              {!finished && (
                <button
                  type="button"
                  className={`button ${deleting ? "danger" : "primary"}`}
                  disabled={
                    loading || !!error || !plan?.targets.some((t) => !t.skip)
                  }
                  onClick={() => void apply()}
                >
                  确认{verb} {(plan?.targets.length || 0) - skipped} 个接入
                </button>
              )}
            </>
          )}
        </footer>
      </Dialog.Content>
    </Dialog.Portal>
  );
}
