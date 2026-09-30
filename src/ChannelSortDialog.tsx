import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  ArrowDown,
  ArrowUp,
  ArrowDownWideNarrow,
  Plus,
  RotateCcw,
  X,
} from "lucide-react";
import { channelSortFields, qualityFirstRules } from "./channelSorting";
import type { ChannelSortRule } from "./channelSorting";
import type { SortTemplate } from "./channelSortPreferences";
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
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ChannelSortRule[]>([]);
  const [chosenTemplate, setChosenTemplate] = useState("");
  const [name, setName] = useState("");
  const [notice, setNotice] = useState("");
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
        if (next) {
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
              ? "已带出此模型上次使用的模板，点击应用排序后用于当前范围。"
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
          <Dialog.Title>渠道排序</Dialog.Title>
          <Dialog.Description>
            仅应用于{scopeLabel}
            ，其他筛选范围保持原样。从上到下依次比较；上一项相同时再比较下一项。仅改变列表显示顺序，不修改实际请求路由。
          </Dialog.Description>
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭排序设置"
          >
            <X size={18} />
          </Dialog.Close>
          {onSaveTemplate && (
            <section
              className="channel-sort-templates"
              aria-label="排序模板管理"
            >
              <label>
                排序模板
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
              </label>
              <label>
                模板名称
                <input
                  aria-label="排序模板名称"
                  placeholder="例如：高级模型质量优先"
                  value={name}
                  onChange={(event) => {
                    setName(event.target.value);
                    setNotice("");
                  }}
                />
              </label>
              <div className="channel-sort-template-actions">
                <button
                  className="button small"
                  disabled={!name.trim() || nameConflict}
                  onClick={() => {
                    const id = onSaveTemplate(name.trim(), draft);
                    setChosenTemplate(id);
                    setNotice("模板已保存，点击应用排序后用于当前范围。");
                  }}
                >
                  保存为模板
                </button>
                <button
                  className="button small"
                  disabled={
                    !chosen ||
                    !name.trim() ||
                    chosen.name === name.trim() ||
                    templates.some(
                      (t) => t.id !== chosen.id && t.name === name.trim(),
                    )
                  }
                  onClick={() => {
                    if (chosen) {
                      onRenameTemplate?.(chosen.id, name.trim());
                      setNotice("模板已重命名。");
                    }
                  }}
                >
                  重命名模板
                </button>
                <button
                  className="button small"
                  disabled={!chosen}
                  onClick={() => {
                    if (chosen) {
                      onDeleteTemplate?.(chosen.id);
                      setChosenTemplate("");
                      setName("");
                      setNotice("模板已删除，各筛选范围已应用的排序保留。");
                    }
                  }}
                >
                  删除模板
                </button>
              </div>
              {chosen && !matchesTemplate && (
                <p>当前规则已调整，可输入新名称另存为模板。</p>
              )}
              {!!name.trim() &&
                nameConflict &&
                (!chosen || chosen.name !== name.trim()) && (
                  <p role="alert">模板名称已存在，请使用其他名称。</p>
                )}
              {notice && <p role="status">{notice}</p>}
              <p>
                模板可跨模型选用，保存在当前浏览器。相同筛选范围恢复已应用方案；此模型进入新的筛选范围时，弹窗带出上次使用的模板。
              </p>
            </section>
          )}
          <div className="channel-sort-presets">
            <button
              className="button small"
              onClick={() =>
                setDraft(qualityFirstRules.map((rule) => ({ ...rule })))
              }
            >
              不降智优先，再按概率
            </button>
            <button className="button small ghost" onClick={() => setDraft([])}>
              <RotateCcw size={14} />
              恢复配置顺序
            </button>
          </div>
          <ol className="channel-sort-rules">
            {draft.map((rule, index) => (
              <li key={rule.field} aria-label={`第 ${index + 1} 优先级`}>
                <span className="channel-sort-priority">{index + 1}</span>
                <label>
                  <span>排序依据</span>
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
                  <span>排序方向</span>
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
                        ? "不降智在前（升序）"
                        : rule.field === "status"
                          ? "可用/冷却优先（升序）"
                          : "从低到高（升序）"}
                    </option>
                    <option value="desc">
                      {rule.field === "quality"
                        ? "降智在前（降序）"
                        : rule.field === "status"
                          ? "临时停用优先（降序）"
                          : "从高到低（降序）"}
                    </option>
                  </select>
                </label>
                <div className="channel-sort-moves">
                  <button
                    className="icon-button"
                    aria-label={`上移第 ${index + 1} 项`}
                    disabled={index === 0}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp size={16} />
                  </button>
                  <button
                    className="icon-button"
                    aria-label={`下移第 ${index + 1} 项`}
                    disabled={index === draft.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown size={16} />
                  </button>
                  <button
                    className="icon-button"
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
            <p className="channel-sort-empty">
              当前按 API key / Provider 配置顺序显示。添加排序依据即可自定义。
            </p>
          )}
          <button
            className="button small"
            disabled={draft.length === channelSortFields.length}
            onClick={() => {
              const field = channelSortFields.find(
                (field) => !draft.some((rule) => rule.field === field.value),
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
          <div className="channel-sort-note">
            <p>
              状态排序将“可用”和“冷却中”视为同一组，“临时停用”单独分组，其他或未知状态排在末尾。放在第一优先级时，可将临时停用渠道集中排在后面，组内继续按后续条件排序。
            </p>
            <p>
              未检测、检测失败、无法判定及缺失数值均排在该项末尾；所有条件相同时保留配置顺序。
            </p>
            <p>
              最近降智结果与累计不降智概率不受时间筛选影响。首字、缓存率、成功率跟随当前时间和模型等筛选；首字使用表中
              P50，成功率按渠道尝试统计。倍率采用已关联站点的计费倍率，未知或多密钥倍率不一致时视为缺失。
            </p>
          </div>
          <footer className="channel-sort-footer">
            <Dialog.Close asChild>
              <button className="button">取消</button>
            </Dialog.Close>
            <button
              className="button primary"
              onClick={() => {
                if (matchesTemplate) onApply(draft, chosen!.id);
                else onApply(draft);
                setOpen(false);
              }}
            >
              应用排序
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
