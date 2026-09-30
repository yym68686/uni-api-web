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
import "./channelSort.css";

export function ChannelSortDialog({
  rules,
  onApply,
}: {
  rules: ChannelSortRule[];
  onApply: (rules: ChannelSortRule[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ChannelSortRule[]>([]);
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
        if (next) setDraft(rules.map((rule) => ({ ...rule })));
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
            从上到下依次比较；上一项相同时再比较下一项。仅改变列表显示顺序，不修改实际请求路由。
          </Dialog.Description>
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭排序设置"
          >
            <X size={18} />
          </Dialog.Close>
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
                        : "从低到高（升序）"}
                    </option>
                    <option value="desc">
                      {rule.field === "quality"
                        ? "降智在前（降序）"
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
                onApply(draft);
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
