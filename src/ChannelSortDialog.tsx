import { useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  ArrowDown,
  ArrowUp,
  ArrowDownWideNarrow,
  Plus,
  RotateCcw,
  Bookmark,
  Check,
  Eye,
  Pencil,
  Save,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { channelSortFields, qualityFirstRules } from "./channelSorting";
import type { ChannelSortRule } from "./channelSorting";
import type { SortTemplate } from "./channelSortPreferences";
import { prepareRouteSorting } from "./routeSorting";
import type { RouteSortPlan, RouteSorting } from "./routeSorting";
import { RouteSortReceipt } from "./RouteSortReceipt";
import type { Channel } from "./types";
import { Spinner } from "./ui";
import "./channelSort.css";

export function ChannelSortDialog({
  rules,
  onApply,
  scopeLabel = "当前筛选范围",
  templates = [],
  templateId,
  suggestedTemplateId,
  onSaveTemplate,
  onRenameTemplate,
  onDeleteTemplate,
  previewRows,
  labelRows,
  routing,
  keyId = "",
  routeApplyDisabled,
  onPreview,
}: {
  rules: ChannelSortRule[];
  onApply: (rules: ChannelSortRule[], templateId?: string) => void;
  scopeLabel?: string;
  templates?: SortTemplate[];
  templateId?: string;
  suggestedTemplateId?: string;
  onSaveTemplate?: (name: string, rules: ChannelSortRule[]) => string;
  onRenameTemplate?: (id: string, name: string) => void;
  onDeleteTemplate?: (id: string) => void;
  previewRows?: (rules: ChannelSortRule[]) => Channel[];
  labelRows?: Channel[];
  routing?: RouteSorting;
  keyId?: string;
  routeApplyDisabled?: boolean;
  onPreview?: (rows: Channel[] | null) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ChannelSortRule[]>([]);
  const [chosenTemplate, setChosenTemplate] = useState("");
  const [name, setName] = useState("");
  const [notice, setNotice] = useState("");
  const [templateAction, setTemplateAction] = useState<
    "save" | "rename" | null
  >(null);
  const [preview, setPreview] = useState<{
    signature: string;
    rules: ChannelSortRule[];
    rows: Channel[];
    plan: RouteSortPlan | null;
  } | null>(null);
  const [loading, setLoading] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const signature = JSON.stringify(draft);
  const currentPreview = preview?.signature === signature ? preview : null;
  function cancelPreview() {
    controller.current?.abort();
    setLoading(false);
    setPreview(null);
    onPreview?.(null);
  }
  const canApply = (plan: RouteSortPlan | null | undefined) =>
    !loading &&
    !routing?.busy &&
    !routeApplyDisabled &&
    !!plan &&
    !plan.errors.length &&
    plan.sources.some((s) => s.changes.length) &&
    !routing?.unresolved;
  function applyPreview(plan: RouteSortPlan) {
    if (!canApply(plan)) return;
    cancelPreview();
    void routing?.apply(plan);
  }
  async function showPreview() {
    if (matchesTemplate) onApply(draft, chosen!.id);
    else onApply(draft);
    const rows = previewRows?.(draft) || [];
    const next = {
      signature,
      rules: draft.map((r) => ({ ...r })),
      rows,
      plan: null,
    };
    controller.current?.abort();
    setLoading(false);
    setPreview(next);
    onPreview?.(rows);
    setOpen(false);
    if (!routing || routeApplyDisabled) return;
    const abort = new AbortController();
    controller.current = abort;
    setLoading(true);
    try {
      const plan = await prepareRouteSorting(
        rows,
        keyId,
        abort.signal,
        labelRows,
      );
      if (!abort.signal.aborted) setPreview({ ...next, plan });
    } finally {
      if (!abort.signal.aborted) setLoading(false);
    }
  }
  const chosen = templates.find((t) => t.id === chosenTemplate);
  const nameConflict = templates.some((t) => t.name === name.trim());
  const matchesTemplate =
    !!chosen && JSON.stringify(chosen.rules) === JSON.stringify(draft);
  function move(index: number, offset: number) {
    setDraft((current) => {
      const next = [...current];
      [next[index], next[index + offset]] = [next[index + offset], next[index]];
      return next;
    });
  }
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (routing?.busy) return;
        if (next) setTemplateAction(null);
        if (next && preview) {
          setDraft(preview.rules.map((rule) => ({ ...rule })));
        } else if (next) {
          const selected = templates.find(
            (t) => t.id === (templateId || suggestedTemplateId),
          );
          setDraft(
            (selected && suggestedTemplateId && !templateId
              ? selected.rules
              : rules
            ).map((rule) => ({ ...rule })),
          );
          setChosenTemplate(selected?.id || "");
          setName(selected?.name || "");
          setNotice(
            selected && suggestedTemplateId && !templateId
              ? "已载入上次使用的模板"
              : "",
          );
        }
        setOpen(next);
      }}
    >
      <Dialog.Trigger asChild>
        <button
          className={`button small ${rules.length ? "selected" : ""}`}
          aria-label="多条件排序"
          title="设置列表排序依据与优先级"
        >
          <ArrowDownWideNarrow size={15} />
          多条件排序
          {rules.length > 0 && (
            <span className="count-badge">{rules.length}</span>
          )}
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog channel-sort-dialog">
          <header className="channel-sort-header">
            <div className="channel-sort-title">
              <span className="channel-sort-title-icon">
                <ArrowDownWideNarrow size={21} />
              </span>
              <Dialog.Title>渠道排序</Dialog.Title>
            </div>
            <Dialog.Close
              className="icon-button channel-sort-close"
              aria-label="关闭排序设置"
              disabled={routing?.busy}
            >
              <X size={19} />
            </Dialog.Close>
            <Dialog.Description
              className="channel-sort-scope"
              title={scopeLabel}
            >
              {scopeLabel
                .split(" · ")
                .filter(
                  (part) =>
                    ![
                      "全部来源",
                      "全部端点",
                      "全部流式状态",
                      "全部状态",
                    ].includes(part),
                )
                .map((part, i) => (
                  <span key={`${part}:${i}`}>{part}</span>
                ))}
            </Dialog.Description>
          </header>
          <div className="channel-sort-body">
            <div
              className={routing?.busy ? "channel-sort-busy" : ""}
              inert={routing?.busy || undefined}
            >
              {onSaveTemplate && (
                <section
                  className="channel-sort-templates"
                  aria-label="排序模板管理"
                >
                  <div className="channel-sort-template-bar">
                    <Bookmark size={16} className="muted" />
                    <select
                      aria-label="排序模板"
                      value={chosenTemplate}
                      onChange={(event) => {
                        const selected = templates.find(
                          (t) => t.id === event.target.value,
                        );
                        setChosenTemplate(selected?.id || "");
                        setName(selected?.name || "");
                        setNotice("");
                        setTemplateAction(null);
                        if (selected)
                          setDraft(selected.rules.map((r) => ({ ...r })));
                      }}
                    >
                      <option value="">自定义方案</option>
                      {templates.map((t) => (
                        <option key={t.id} value={t.id}>
                          {t.name}
                        </option>
                      ))}
                    </select>
                    {chosen && !matchesTemplate && (
                      <span className="channel-sort-modified">已调整</span>
                    )}
                    <div className="channel-sort-template-actions">
                      <button
                        className="button small"
                        aria-label="保存为模板"
                        title="另存为模板"
                        onClick={() => {
                          setTemplateAction("save");
                          setName("");
                          setNotice("");
                        }}
                      >
                        <Save size={14} />
                        存为模板
                      </button>
                      <button
                        className="icon-button"
                        aria-label="重命名模板"
                        title="重命名模板"
                        disabled={!chosen}
                        onClick={() => {
                          setTemplateAction("rename");
                          setName(chosen!.name);
                          setNotice("");
                        }}
                      >
                        <Pencil size={15} />
                      </button>
                      <button
                        className="icon-button channel-sort-remove"
                        aria-label="删除模板"
                        title="删除模板"
                        disabled={!chosen}
                        onClick={() => {
                          if (chosen) {
                            onDeleteTemplate?.(chosen.id);
                            setChosenTemplate("");
                            setName("");
                            setTemplateAction(null);
                            setNotice("模板已删除");
                          }
                        }}
                      >
                        <Trash2 size={15} />
                      </button>
                    </div>
                  </div>
                  {templateAction && (
                    <form
                      className="channel-sort-template-editor"
                      onSubmit={(event) => {
                        event.preventDefault();
                        const nextName = name.trim();
                        if (
                          !nextName ||
                          templates.some(
                            (t) =>
                              t.name === nextName &&
                              (templateAction === "save" ||
                                t.id !== chosen?.id),
                          )
                        )
                          return;
                        if (templateAction === "save") {
                          setChosenTemplate(onSaveTemplate(nextName, draft));
                          setNotice("模板已保存");
                        } else if (chosen) {
                          onRenameTemplate?.(chosen.id, nextName);
                          setNotice("模板已重命名");
                        }
                        setTemplateAction(null);
                      }}
                    >
                      <input
                        autoFocus
                        aria-label="排序模板名称"
                        placeholder={
                          templateAction === "rename"
                            ? "新的模板名称"
                            : "模板名称"
                        }
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                      />
                      <button
                        className="button small"
                        type="submit"
                        disabled={
                          !name.trim() ||
                          (templateAction === "save"
                            ? nameConflict
                            : !chosen ||
                              chosen.name === name.trim() ||
                              templates.some(
                                (t) =>
                                  t.id !== chosen.id && t.name === name.trim(),
                              ))
                        }
                      >
                        <Check size={14} />
                        保存
                      </button>
                      <button
                        className="icon-button"
                        type="button"
                        aria-label="取消模板编辑"
                        title="取消"
                        onClick={() => setTemplateAction(null)}
                      >
                        <X size={15} />
                      </button>
                      {!!name.trim() &&
                        templates.some(
                          (t) =>
                            t.name === name.trim() &&
                            (templateAction === "save" || t.id !== chosen?.id),
                        ) && <p role="alert">模板名称已存在</p>}
                    </form>
                  )}
                  {notice && (
                    <p role="status" className="channel-sort-notice">
                      {notice}
                    </p>
                  )}
                </section>
              )}
              <section
                className="channel-sort-rule-section"
                aria-label="排序规则"
              >
                <div className="channel-sort-rule-heading">
                  <h3>
                    排序规则 <span>{draft.length}</span>
                  </h3>
                  <div className="channel-sort-presets">
                    <button
                      className="button small ghost"
                      aria-label="不降智优先，再按概率"
                      title="先按最近检测结果，再按不降智概率"
                      onClick={() =>
                        setDraft(qualityFirstRules.map((rule) => ({ ...rule })))
                      }
                    >
                      <Sparkles size={14} />
                      质量优先
                    </button>
                    <button
                      className="icon-button"
                      aria-label="恢复配置顺序"
                      title="清空规则"
                      disabled={!draft.length}
                      onClick={() => setDraft([])}
                    >
                      <RotateCcw size={15} />
                    </button>
                  </div>
                </div>
                {!!draft.length && (
                  <div className="channel-sort-columns" aria-hidden="true">
                    <span>#</span>
                    <span>排序依据</span>
                    <span>方向</span>
                    <span>优先级</span>
                  </div>
                )}
                <ol className="channel-sort-rules">
                  {draft.map((rule, index) => (
                    <li key={rule.field} aria-label={`第 ${index + 1} 优先级`}>
                      <span className="channel-sort-priority">
                        {String(index + 1).padStart(2, "0")}
                      </span>
                      <label>
                        <select
                          aria-label={`第 ${index + 1} 排序依据`}
                          value={rule.field}
                          onChange={(event) => {
                            const selected = channelSortFields.find(
                              (field) => field.value === event.target.value,
                            )!;
                            setDraft((current) =>
                              current.map((r, i) =>
                                i === index
                                  ? {
                                      field: selected.value,
                                      direction: selected.direction,
                                    }
                                  : r,
                              ),
                            );
                          }}
                        >
                          {channelSortFields.map((field) => (
                            <option
                              key={field.value}
                              value={field.value}
                              disabled={
                                field.value !== rule.field &&
                                draft.some((r) => r.field === field.value)
                              }
                            >
                              {field.label}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label>
                        <select
                          aria-label={`第 ${index + 1} 排序方向`}
                          value={rule.direction}
                          onChange={(event) =>
                            setDraft((current) =>
                              current.map((r, i) =>
                                i === index
                                  ? {
                                      ...r,
                                      direction: event.target
                                        .value as ChannelSortRule["direction"],
                                    }
                                  : r,
                              ),
                            )
                          }
                        >
                          <option value="asc">
                            {rule.field === "quality"
                              ? "不降智优先"
                              : rule.field === "status"
                                ? "可用 / 冷却优先"
                                : "从低到高 ↑"}
                          </option>
                          <option value="desc">
                            {rule.field === "quality"
                              ? "降智优先"
                              : rule.field === "status"
                                ? "临时停用优先"
                                : "从高到低 ↓"}
                          </option>
                        </select>
                      </label>
                      <div className="channel-sort-moves">
                        <button
                          className="icon-button"
                          aria-label={`上移第 ${index + 1} 项`}
                          title="提高优先级"
                          disabled={index === 0}
                          onClick={() => move(index, -1)}
                        >
                          <ArrowUp size={16} />
                        </button>
                        <button
                          className="icon-button"
                          aria-label={`下移第 ${index + 1} 项`}
                          title="降低优先级"
                          disabled={index === draft.length - 1}
                          onClick={() => move(index, 1)}
                        >
                          <ArrowDown size={16} />
                        </button>
                        <button
                          className="icon-button channel-sort-remove"
                          title="删除规则"
                          aria-label={`删除第 ${index + 1} 项`}
                          onClick={() =>
                            setDraft((current) =>
                              current.filter((_, i) => i !== index),
                            )
                          }
                        >
                          <X size={16} />
                        </button>
                      </div>
                    </li>
                  ))}
                </ol>
                {!draft.length && (
                  <div className="channel-sort-empty">
                    <ArrowDownWideNarrow size={25} strokeWidth={1.5} />
                    <strong>按实际路由顺序</strong>
                    <span>添加规则，自定义渠道优先级</span>
                  </div>
                )}
                <button
                  className="button channel-sort-add"
                  disabled={draft.length === channelSortFields.length}
                  onClick={() => {
                    const field = channelSortFields.find(
                      (field) =>
                        !draft.some((rule) => rule.field === field.value),
                    );
                    if (field)
                      setDraft([
                        ...draft,
                        { field: field.value, direction: field.direction },
                      ]);
                  }}
                >
                  <Plus size={14} />
                  添加排序依据
                </button>
              </section>
            </div>
            <div className="channel-sort-feedback" aria-live="polite">
              {loading && (
                <p role="status">
                  <Spinner small />
                  正在核对路由…
                </p>
              )}
              {preview && !currentPreview && (
                <p role="status">规则已更改，请重新预览</p>
              )}
              {currentPreview && <p role="status">预览中 · 尚未应用</p>}
              {currentPreview?.plan?.errors.map((error) => (
                <p role="alert" className="negative" key={error}>
                  {error}
                </p>
              ))}
              {routeApplyDisabled && (
                <p className="muted">数据未就绪或有未保存的调序，暂不可应用</p>
              )}
            </div>
            {routing && <RouteSortReceipt routing={routing} />}
          </div>
          <footer className="channel-sort-footer">
            <span className="channel-sort-footer-hint">
              {draft.length
                ? `${draft.length} 项规则 · 上方优先`
                : "实际路由顺序"}
            </span>
            <div className="channel-sort-footer-actions">
              <button
                className="button"
                disabled={routing?.busy}
                onClick={() => {
                  if (currentPreview) {
                    cancelPreview();
                  } else {
                    void showPreview();
                  }
                }}
              >
                {currentPreview ? <X size={15} /> : <Eye size={15} />}
                {currentPreview ? "取消预览" : "预览"}
              </button>
              {routing && (
                <button
                  className="button primary"
                  title={
                    canApply(currentPreview?.plan)
                      ? "写入 uni-api 请求顺序"
                      : "请先预览，再应用到真实路由"
                  }
                  disabled={!canApply(currentPreview?.plan)}
                  onClick={() => applyPreview(currentPreview!.plan!)}
                >
                  {routing.busy ? (
                    <>
                      <Spinner small />
                      处理中
                    </>
                  ) : (
                    <>
                      <Check size={15} />
                      应用排序
                    </>
                  )}
                </button>
              )}
            </div>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
      {preview && !open && (
        <>
          <button
            className="button small selected"
            disabled={routing?.busy}
            onClick={cancelPreview}
          >
            <X size={14} />
            取消预览
          </button>
          {routing && (
            <button
              className="button small primary"
              title="按当前预览顺序请求"
              disabled={!canApply(preview.plan)}
              onClick={() => applyPreview(preview.plan!)}
            >
              {loading ? (
                <>
                  <Spinner small />
                  核对路由中
                </>
              ) : (
                "应用排序"
              )}
            </button>
          )}
          {preview.plan?.errors.map((error) => (
            <span role="alert" className="negative" key={error}>
              {error}
            </span>
          ))}
          {preview.plan &&
            !preview.plan.errors.length &&
            !preview.plan.sources.some((s) => s.changes.length) && (
              <span role="status" className="muted">
                顺序与实际路由一致
              </span>
            )}
        </>
      )}
    </Dialog.Root>
  );
}
