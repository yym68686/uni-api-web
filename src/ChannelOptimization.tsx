import { useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles, X } from "./icons";
import { Spinner } from "./ui";
import { rate } from "./format";
import { channelEngineLabel, channelProtocolLabel } from "./channelProtocol";
import {
  applyOptimization,
  buildOptimizationPlan,
  optimizationEvents,
  optimizationTarget,
} from "./channelOptimizationPlan";
import type {
  OptimizationChange,
  OptimizationEvent,
  OptimizationSnapshot,
} from "./channelOptimizationPlan";
import "./channelOptimization.css";

export function ChannelOptimization({
  snapshot,
  disabled,
}: {
  snapshot: () => OptimizationSnapshot | Promise<OptimizationSnapshot>;
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
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState("");
  const readingRef = useRef(false);
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
  const eligible = plan.changes.filter((c) => !c.blockedReason);
  function selectChanges(items: OptimizationChange[], checked: boolean) {
    setSelected((old) => {
      const next = new Set(old);
      for (const change of items) {
        if (checked) {
          next.add(change.id);
          if (change.requiresLegacyMigration)
            for (const c of eligible)
              if (
                c.binding.id === optimizationTarget(change).id &&
                c.action === "migrate"
              )
                next.add(c.id);
        } else {
          next.delete(change.id);
          if (change.action === "migrate")
            for (const c of eligible)
              if (
                optimizationTarget(c).id === change.binding.id &&
                c.requiresLegacyMigration
              )
                next.delete(c.id);
        }
      }
      return next;
    });
  }
  const selectedMembership = new Map<string, Set<string>>();
  for (const change of eligible) {
    if (!selected.has(change.id)) continue;
    const update = (binding: OptimizationChange["binding"], add: boolean) => {
      const kept =
        selectedMembership.get(binding.id) ||
        new Set(Object.keys(binding.current));
      if (add) kept.add(change.model);
      else kept.delete(change.model);
      selectedMembership.set(binding.id, kept);
    };
    if (change.action === "remove" || change.action === "switch")
      update(change.binding, false);
    if (
      !change.splitProtocol &&
      (change.action === "add" || change.action === "switch")
    )
      update(optimizationTarget(change), true);
  }
  const tierConflicts = new Map<string, string>();
  for (const change of eligible) {
    if (!change.tier || !selected.has(change.id)) continue;
    const remaining = selectedMembership.get(change.binding.id);
    if (remaining?.size === 0) {
      tierConflicts.set(change.id, "此接入将被移除，请取消该档位变更。");
    } else if (
      remaining &&
      change.action === "tier-add" &&
      !change.tierEvidenceModels?.some((model) => remaining.has(model))
    ) {
      tierConflicts.set(
        change.id,
        "已验证档位的模型将离开此接入，请取消该档位变更。",
      );
    }
  }
  async function open() {
    if (readingRef.current) return;
    readingRef.current = true;
    setReading(true);
    try {
      setInput(structuredClone(await snapshot()));
      setReadError("");
    } catch (error) {
      setReadError(error instanceof Error ? error.message : "优化数据读取失败");
      return;
    } finally {
      readingRef.current = false;
      setReading(false);
    }
    setSelected(new Set());
    setFinished(false);
    setError("");
    setProgress({});
  }
  async function apply() {
    if (
      !input ||
      locked.current ||
      finished ||
      !selected.size ||
      tierConflicts.size
    )
      return;
    locked.current = true;
    setBusy(true);
    setError("");
    try {
      await applyOptimization(
        eligible.filter((c) => selected.has(c.id)),
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
          "management-query",
          "service-tier-members",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
    }
  }
  return (
    <Dialog.Root
      open={!!input || !!readError}
      onOpenChange={(next) => {
        if (busy || reading) return;
        if (next) open();
        else {
          setInput(null);
          setReadError("");
        }
      }}
    >
      <Dialog.Trigger asChild>
        <button
          className="button small"
          disabled={disabled || reading}
          aria-label="优化渠道模型"
          aria-busy={reading}
        >
          {reading ? <Spinner small /> : <Sparkles size={15} />}
          优化
        </button>
      </Dialog.Trigger>
      {readError && (
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="guide-dialog channel-optimization-dialog optimization-read-error">
            <header>
              <Dialog.Title>优化预览读取失败</Dialog.Title>
              <Dialog.Description>
                渠道配置未修改。可以重新读取当前筛选范围。
              </Dialog.Description>
              <Dialog.Close
                className="icon-button detail-close"
                disabled={reading}
                aria-label="关闭优化错误"
              >
                <X size={18} />
              </Dialog.Close>
            </header>
            <div className="channel-optimization-body negative" role="alert">
              {readError}
            </div>
            <footer>
              <Dialog.Close className="button small" disabled={reading}>
                关闭
              </Dialog.Close>
              <button
                className="button primary small"
                disabled={disabled || reading}
                aria-busy={reading}
                onClick={() => void open()}
              >
                {reading && <Spinner small />}重新读取
              </button>
            </footer>
          </Dialog.Content>
        </Dialog.Portal>
      )}
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
                分析当前筛选的全部 {input.rows.length} 个渠道
                {input.filters.model && ` · ${input.filters.model}`}
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
              <details className="optimization-help">
                <summary>优化规则</summary>
                <p>
                  新增模型须已检测可用，且没有已确认的单价、模型匹配、指纹、Tool
                  use、降智或压缩异常；Tool use
                  检测失败时也不新增。未确认不当作正常，未检测和检测中的模型不会因缺少结果被移除。降智仅作用于
                  Astra，压缩仅作用于实际检测的模型。取消勾选后，原模型及指向它的别名路由也会列入预览。
                </p>
                <p>
                  预览使用点击时读取的最新检测结果。仅优化已接入的渠道；新增模型放在路由末尾。
                </p>
                <p>
                  协议冲突时优先复用同一分组、同一 Key
                  下已有的目标协议接入；没有时才新建。切换已接入模型会同时移除它的旧路由，保留未勾选模型及其协议。指纹只依据本轮完整结果，不使用样本不足时展示的上次结果。
                </p>
                <p>
                  档位变更作用于整个接入。新增只依据业务凭据的有效验证；取消要求相关模型都明确不支持或未命中。
                </p>
                <p>现有路由的相对顺序和自定义设置不变。</p>
              </details>
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
              {Object.entries(progress)
                .filter(([, state]) => state.state === "error")
                .map(([source, state]) => (
                  <p className="error-banner" role="alert" key={source}>
                    {groups.find(
                      (changes) => changes[0].binding.source === source,
                    )?.[0].binding.sourceName || source}
                    ：本批次优化未确认。{state.message}
                    所选渠道属于同一批次，此提示不代表每个渠道都未通过检测。请刷新核对后重新分析。
                  </p>
                ))}
              <div className="optimization-selection">
                <label>
                  <input
                    type="checkbox"
                    aria-label="选择全部优化变更"
                    disabled={busy || finished || !eligible.length}
                    checked={
                      !!eligible.length &&
                      eligible.every((c) => selected.has(c.id))
                    }
                    onChange={(e) =>
                      setSelected(
                        e.target.checked
                          ? new Set(eligible.map((c) => c.id))
                          : new Set(),
                      )
                    }
                  />
                  全选可应用项
                </label>
                <span>
                  {groups.length} 个接入 ·{" "}
                  {eligible.filter((c) => c.action === "add").length} 项新增 ·{" "}
                  {eligible.filter((c) => c.action === "remove").length} 项取消
                  {!!eligible.filter((c) => c.action === "switch").length &&
                    ` · ${eligible.filter((c) => c.action === "switch").length} 项协议切换`}
                  {!!eligible.filter((c) => c.action === "migrate").length &&
                    ` · ${eligible.filter((c) => c.action === "migrate").length} 项协议升级`}
                  {!!eligible.filter((c) => c.tier).length &&
                    ` · ${eligible.filter((c) => c.tier).length} 项档位变更`}
                  {plan.changes.length > eligible.length &&
                    ` · ${plan.changes.length - eligible.length} 项需处理`}
                </span>
              </div>
              {!plan.changes.length && (
                <p className="optimization-empty">没有可优化的已接入模型。</p>
              )}
              {groups.map((changes) => {
                const b = changes[0].binding,
                  state = progress[b.source];
                const keyLabel = b.keyPosition
                  ? `Key ${b.keyPosition}`
                  : "全部 Key";
                const selectable = changes.filter((c) => !c.blockedReason);
                const keptCount =
                  selectedMembership.get(b.id)?.size ??
                  Object.keys(b.current).length;
                return (
                  <section className="optimization-binding" key={b.id}>
                    <header>
                      <label>
                        <input
                          type="checkbox"
                          aria-label={`选择 ${b.sourceName} ${keyLabel} ${b.name} 的优化变更`}
                          disabled={busy || finished || !selectable.length}
                          checked={
                            !!selectable.length &&
                            selectable.every((c) => selected.has(c.id))
                          }
                          onChange={(e) =>
                            selectChanges(selectable, e.target.checked)
                          }
                        />
                        <strong>{b.name}</strong>
                      </label>
                      <span>
                        {b.sourceName} · {keyLabel}
                      </span>
                    </header>
                    <div className="optimization-protocol">
                      <strong>
                        当前协议：{channelProtocolLabel(b.protocol)}
                      </strong>
                      <span>
                        {channelEngineLabel({
                          engine: b.engine,
                          engine_mode: b.engineMode,
                          engine_origin: b.engineOrigin,
                        }) || "未取得实际引擎"}
                      </span>
                      <small>渠道标识：{b.provider}</small>
                    </div>
                    {changes.map((c) => (
                      <label
                        className={`optimization-change${c.blockedReason ? " optimization-blocked" : ""}`}
                        key={c.id}
                      >
                        <input
                          type="checkbox"
                          aria-label={`${c.action === "migrate" ? "升级旧版默认协议" : `${c.action === "switch" ? "切换协议" : c.action === "add" || c.action === "tier-add" ? "新增" : "取消"} ${c.model}`} · ${b.sourceName} ${keyLabel} ${b.name}`}
                          checked={selected.has(c.id)}
                          disabled={busy || finished || !!c.blockedReason}
                          onChange={(e) => selectChanges([c], e.target.checked)}
                        />
                        <span
                          className={
                            c.action !== "remove" && c.action !== "tier-remove"
                              ? "positive"
                              : "negative"
                          }
                        >
                          {c.action === "migrate"
                            ? "协议 · 升级"
                            : c.action === "switch"
                              ? "协议 · 切换"
                              : c.reuseBinding
                                ? "协议 · 复用接入"
                                : c.splitProtocol
                                  ? "协议 · 新接入"
                                  : c.tier
                                    ? c.action === "tier-add"
                                      ? "接入档位 · 新增"
                                      : "接入档位 · 取消"
                                    : c.action === "add"
                                      ? "模型 · 新增"
                                      : "模型 · 取消"}
                        </span>
                        <span>
                          <strong>
                            {c.action === "migrate"
                              ? "旧版默认 gpt → 自动识别 codex"
                              : c.tier
                                ? `整个接入 · ${c.tier}`
                                : c.model}
                          </strong>
                          {c.model !== c.upstream && (
                            <small>原模型：{c.upstream}</small>
                          )}
                          {c.action === "switch" && (
                            <small>
                              {channelProtocolLabel(b.protocol)} →{" "}
                              {channelProtocolLabel(c.detectedProtocols?.[0])}
                            </small>
                          )}
                          {c.reuseBinding ? (
                            <small>
                              目标接入：{c.reuseBinding.name} ·{" "}
                              {channelProtocolLabel(c.detectedProtocols?.[0])}
                            </small>
                          ) : c.splitProtocol ? (
                            <small>
                              新接入协议：
                              {channelProtocolLabel(c.splitProtocol)}
                            </small>
                          ) : (
                            c.action !== "migrate" &&
                            !c.tier && (
                              <small>
                                检测协议：
                                {c.detectedProtocols?.length
                                  ? c.detectedProtocols
                                      .map(channelProtocolLabel)
                                      .join(" / ")
                                  : "未记录"}
                              </small>
                            )
                          )}
                        </span>
                        <span>
                          {c.reasons.join("；")}
                          {!!c.returnedModels?.length && (
                            <small>
                              返回模型：{c.returnedModels.join("、")}
                            </small>
                          )}
                          {c.fingerprintDetails?.map((detail) => (
                            <span
                              className="optimization-fingerprint"
                              key={detail.id}
                            >
                              {(c.fingerprintDetails?.length ?? 0) > 1 && (
                                <small>检测来源：{detail.scope}</small>
                              )}
                              <small>
                                请求模型的匹配概率：{rate(detail.probability)}
                              </small>
                              {detail.candidates.length > 0 && (
                                <>
                                  <small>参考库候选概率</small>
                                  {detail.candidates.map((candidate) => (
                                    <small
                                      className="optimization-fingerprint-row"
                                      key={candidate.model}
                                    >
                                      <span title={candidate.model}>
                                        {candidate.display_name ||
                                          candidate.model}
                                      </span>
                                      <span>{rate(candidate.probability)}</span>
                                    </small>
                                  ))}
                                </>
                              )}
                            </span>
                          ))}
                          {c.blockedReason && (
                            <strong className="optimization-blocked-reason">
                              {c.blockedReason}
                            </strong>
                          )}
                          {tierConflicts.has(c.id) && (
                            <strong
                              className="optimization-blocked-reason"
                              role="alert"
                            >
                              {tierConflicts.get(c.id)}
                            </strong>
                          )}
                          {!!c.priceDetails?.length && (
                            <>
                              <small>
                                倍率前单价 · 美元／百万 token · 应为价格设置值
                              </small>
                              {c.priceDetails.map((detail) => (
                                <small
                                  className="optimization-price-detail"
                                  key={detail}
                                >
                                  {detail}
                                </small>
                              ))}
                            </>
                          )}
                          <small>
                            {c.checkedAt
                              ? `检测于 ${new Date(c.checkedAt * 1000).toLocaleString("zh-CN", { hour12: false })}`
                              : ""}
                          </small>
                        </span>
                      </label>
                    ))}
                    {!keptCount && (
                      <p className="coverage-note">
                        所选变更后原接入不再有模型，将从此 API key 移除。
                      </p>
                    )}
                    {state &&
                      state.state !== "error" &&
                      changes.some((c) => selected.has(c.id)) && (
                        <p role="status" className="optimization-result">
                          {state.state === "running" ? "正在应用…" : "已应用"}
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
                disabled={
                  busy || finished || !selected.size || !!tierConflicts.size
                }
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
