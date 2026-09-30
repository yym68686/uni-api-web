import { useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles, X } from "lucide-react";
import { Spinner } from "./ui";
import {
  applyOptimization,
  buildOptimizationPlan,
  optimizationEvents,
} from "./channelOptimizationPlan";
import type {
  OptimizationEvent,
  OptimizationSnapshot,
} from "./channelOptimizationPlan";
import "./channelOptimization.css";

export function ChannelOptimization({
  snapshot,
  disabled,
}: {
  snapshot: () => OptimizationSnapshot;
  disabled?: boolean;
}) {
  const client = useQueryClient();
  const [input, setInput] = useState<OptimizationSnapshot | null>(null);
  const [events, setEvents] = useState<OptimizationEvent[]>(
    optimizationEvents.map((e) => e.id),
  );
  const [selected, setSelected] = useState(new Set<string>());
  const [busy, setBusy] = useState(false),
    [finished, setFinished] = useState(false),
    [error, setError] = useState("");
  const [progress, setProgress] = useState<
    Record<string, { state: string; message?: string }>
  >({});
  const locked = useRef(false);
  const plan = useMemo(
    () =>
      input ? buildOptimizationPlan(input, events) : { changes: [], notes: [] },
    [input, events],
  );
  const groups = useMemo(() => {
    const result = new Map<string, typeof plan.changes>();
    for (const c of plan.changes)
      result.set(c.binding.id, [...(result.get(c.binding.id) || []), c]);
    return [...result.values()];
  }, [plan]);
  function open() {
    setInput(structuredClone(snapshot()));
    setSelected(new Set());
    setFinished(false);
    setError("");
    setProgress({});
  }
  async function apply() {
    if (!input || locked.current || finished || !selected.size) return;
    locked.current = true;
    setBusy(true);
    setError("");
    try {
      await applyOptimization(
        plan.changes.filter((c) => selected.has(c.id)),
        input.routes,
        (source, state, message) =>
          setProgress((old) => ({ ...old, [source]: { state, message } })),
      );
      setFinished(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "优化未完成");
      setFinished(true);
    } finally {
      locked.current = false;
      setBusy(false);
      void Promise.all(
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
    }
  }
  return (
    <Dialog.Root
      open={!!input}
      onOpenChange={(next) => {
        if (busy) return;
        if (next) open();
        else setInput(null);
      }}
    >
      <Dialog.Trigger asChild>
        <button
          className="button small"
          disabled={disabled}
          aria-label="优化渠道模型"
        >
          <Sparkles size={15} />
          优化
        </button>
      </Dialog.Trigger>
      {input && (
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content
            className="guide-dialog channel-optimization-dialog"
            onPointerDownOutside={(e) => e.preventDefault()}
            onEscapeKeyDown={(e) => {
              if (busy) e.preventDefault();
            }}
          >
            <header>
              <Dialog.Title>优化渠道模型</Dialog.Title>
              <Dialog.Description>
                分析当前筛选的全部 {input.rows.length}{" "}
                个渠道（包含其他页），仅修改已经接入 API key 的渠道。
                {input.filters.model
                  ? `本次仅优化 ${input.filters.model}。`
                  : "本次分析各渠道符合筛选条件的模型。"}
              </Dialog.Description>
              <Dialog.Close
                className="icon-button detail-close"
                disabled={busy}
                aria-label="关闭优化预览"
              >
                <X size={18} />
              </Dialog.Close>
            </header>
            <div className="channel-optimization-body">
              <fieldset
                className="optimization-events"
                disabled={busy || finished}
              >
                <legend>优化事件</legend>
                {optimizationEvents.map((event) => (
                  <label key={event.id}>
                    <input
                      type="checkbox"
                      checked={events.includes(event.id)}
                      onChange={(e) => {
                        setEvents((old) =>
                          e.target.checked
                            ? [...old, event.id]
                            : old.filter((id) => id !== event.id),
                        );
                        setSelected(new Set());
                      }}
                    />
                    {event.label}
                  </label>
                ))}
              </fieldset>
              <p className="optimization-help">
                新增模型须已检测可用，且没有已确认的单价、模型匹配、Tool
                use、降智或压缩异常；Tool use
                检测失败时也不新增。未确认不当作正常，未检测和检测中的模型不会因缺少结果被移除。降智仅作用于
                Astra，压缩仅作用于实际检测的模型。取消勾选后，原模型及指向它的别名路由也会列入预览。
              </p>
              <p className="optimization-help">
                新增模型放在对应模型路由末尾，现有模型的相对顺序及渠道自定义设置保留。所有变更均需勾选后应用。
              </p>
              {plan.notes.map((note) => (
                <p className="coverage-note" role="status" key={note}>
                  {note}
                </p>
              ))}
              {error && (
                <p className="error-banner" role="alert">
                  {error}
                </p>
              )}
              <div className="optimization-selection">
                <label>
                  <input
                    type="checkbox"
                    aria-label="选择全部优化变更"
                    disabled={busy || finished || !plan.changes.length}
                    checked={
                      !!plan.changes.length &&
                      selected.size === plan.changes.length
                    }
                    onChange={(e) =>
                      setSelected(
                        e.target.checked
                          ? new Set(plan.changes.map((c) => c.id))
                          : new Set(),
                      )
                    }
                  />
                  全选
                </label>
                <span>
                  {groups.length} 个接入 ·{" "}
                  {plan.changes.filter((c) => c.action === "add").length} 项新增
                  · {plan.changes.filter((c) => c.action === "remove").length}{" "}
                  项取消
                </span>
              </div>
              {!plan.changes.length && (
                <p className="optimization-empty">
                  当前筛选范围内没有可优化的已接入模型。未加入任何 API key
                  的渠道不会自动创建接入；可调整页面筛选或先完成检测。
                </p>
              )}
              {groups.map((changes) => {
                const b = changes[0].binding,
                  state = progress[b.source];
                const kept = new Set(Object.keys(b.current));
                for (const c of changes)
                  if (selected.has(c.id)) {
                    if (c.action === "add") kept.add(c.model);
                    else kept.delete(c.model);
                  }
                return (
                  <section className="optimization-binding" key={b.id}>
                    <header>
                      <label>
                        <input
                          type="checkbox"
                          aria-label={`选择 ${b.sourceName} Key ${b.keyPosition} ${b.name} 的优化变更`}
                          disabled={busy || finished}
                          checked={changes.every((c) => selected.has(c.id))}
                          onChange={(e) =>
                            setSelected((old) => {
                              const next = new Set(old);
                              for (const c of changes) {
                                if (e.target.checked) next.add(c.id);
                                else next.delete(c.id);
                              }
                              return next;
                            })
                          }
                        />
                        <strong>{b.name}</strong>
                      </label>
                      <span>
                        {b.sourceName} · Key {b.keyPosition}
                      </span>
                    </header>
                    {changes.map((c) => (
                      <label className="optimization-change" key={c.id}>
                        <input
                          type="checkbox"
                          aria-label={`${c.action === "add" ? "新增" : "取消"} ${c.model} · ${b.sourceName} Key ${b.keyPosition} ${b.name}`}
                          checked={selected.has(c.id)}
                          disabled={busy || finished}
                          onChange={(e) =>
                            setSelected((old) => {
                              const next = new Set(old);
                              if (e.target.checked) next.add(c.id);
                              else next.delete(c.id);
                              return next;
                            })
                          }
                        />
                        <span
                          className={
                            c.action === "add" ? "positive" : "negative"
                          }
                        >
                          {c.action === "add" ? "新增勾选" : "取消勾选"}
                        </span>
                        <span>
                          <strong>{c.model}</strong>
                          {c.model !== c.upstream && (
                            <small>原模型：{c.upstream}</small>
                          )}
                        </span>
                        <span>
                          {c.reasons.join("；")}
                          <small>
                            {c.checkedAt
                              ? `检测于 ${new Date(c.checkedAt * 1000).toLocaleString("zh-CN", { hour12: false })}`
                              : ""}
                          </small>
                        </span>
                      </label>
                    ))}
                    {!kept.size && (
                      <p className="coverage-note">
                        所选变更将取消最后一个模型：此 API key
                        将移除该渠道接入。
                      </p>
                    )}
                    {state && changes.some((c) => selected.has(c.id)) && (
                      <p
                        role={state.state === "error" ? "alert" : "status"}
                        className={
                          state.state === "error"
                            ? "negative"
                            : "optimization-result"
                        }
                      >
                        {state.state === "running"
                          ? "正在应用…"
                          : state.state === "done"
                            ? "已应用"
                            : `未确认：${state.message}`}
                      </p>
                    )}
                  </section>
                );
              })}
            </div>
            <footer>
              <span>已选 {selected.size} 项变更</span>
              <Dialog.Close asChild>
                <button className="button" disabled={busy}>
                  {finished ? "完成" : "取消"}
                </button>
              </Dialog.Close>
              <button
                className="button primary"
                disabled={busy || finished || !selected.size}
                onClick={() => void apply()}
              >
                {busy ? (
                  <>
                    <Spinner small />
                    正在应用
                  </>
                ) : (
                  "一键应用优化"
                )}
              </button>
            </footer>
            {finished && (
              <p className="optimization-help">
                需要重试时，请关闭弹窗、刷新渠道管理后重新分析，避免重复提交已应用的变更。
              </p>
            )}
          </Dialog.Content>
        </Dialog.Portal>
      )}
    </Dialog.Root>
  );
}
