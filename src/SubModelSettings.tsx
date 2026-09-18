import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Settings2, X } from "lucide-react";
import { SUB_MODELS, subModelProtocolLabel } from "./sub2apiModels";
import type { SubModel } from "./sub2apiModels";

export function SubModelSettings({
  models,
  onSave,
}: {
  models: SubModel[];
  onSave: (models: SubModel[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(models);
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (next) setDraft([...models]);
        setOpen(next);
      }}
    >
      <Dialog.Trigger asChild>
        <button className="button small" aria-label="检测模型设置">
          <Settings2 size={15} />
          设置
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog sub-model-settings">
          <Dialog.Title>检测模型设置</Dialog.Title>
          <Dialog.Description>
            选择模型检测区域要检测的模型，选择会自动记住。筛选单个模型时，仅检测已勾选的该模型。
          </Dialog.Description>
          <Dialog.Close asChild>
            <button
              className="icon-button detail-close"
              aria-label="关闭模型设置"
            >
              <X size={18} />
            </button>
          </Dialog.Close>
          <div className="sub-model-settings-actions">
            <span>
              已选 {draft.length} / {SUB_MODELS.length}
            </span>
            <button
              className="button small"
              onClick={() => setDraft([...SUB_MODELS])}
            >
              全选
            </button>
            <button className="button small" onClick={() => setDraft([])}>
              清空
            </button>
          </div>
          <div className="sub-model-settings-options">
            {SUB_MODELS.map((model) => (
              <label key={model}>
                <input
                  type="checkbox"
                  aria-label={model}
                  checked={draft.includes(model)}
                  onChange={(event) => {
                    setDraft(
                      event.target.checked
                        ? SUB_MODELS.filter(
                            (item) => item === model || draft.includes(item),
                          )
                        : draft.filter((item) => item !== model),
                    );
                  }}
                />
                <span>
                  {model}
                  <small>{subModelProtocolLabel(model)}</small>
                </span>
              </label>
            ))}
          </div>
          {!draft.length && <p role="status">请至少选择一个模型。</p>}
          <div className="sub-model-settings-actions">
            <Dialog.Close asChild>
              <button className="button">取消</button>
            </Dialog.Close>
            <button
              className="button primary"
              disabled={!draft.length}
              onClick={() => {
                onSave(draft);
                setOpen(false);
              }}
            >
              保存设置
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
