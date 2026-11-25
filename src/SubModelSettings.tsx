import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Settings2 } from "./icons";
import { CompactDialogHeader } from "./CompactDialog";
import { SUB_MODELS, subModelProtocolLabel } from "./sub2apiModels";
import type { SubModel } from "./sub2apiModels";

export function SubModelSettings({
  models,
  extraModels = [],
  onSave,
}: {
  models: SubModel[];
  extraModels?: string[];
  onSave: (models: SubModel[]) => void;
}) {
  const extras = [...new Set(extraModels)].filter(model => !SUB_MODELS.includes(model));
  const available = [...SUB_MODELS, ...extras];
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
        <button className="button small sub-check-icon" aria-label="检测模型设置" title="检测模型设置">
          <Settings2 size={15} />
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog compact-dialog sub-model-settings">
          <CompactDialogHeader context="用于账号同步检测和未指定模型的批量检测。" closeLabel="关闭模型设置">检测模型设置</CompactDialogHeader>
          <div className="compact-dialog-scroll">
          <div className="sub-model-settings-actions" role="group" aria-label="内置模型选择">
            <span>内置模型 · 已选 {SUB_MODELS.filter(model=>draft.includes(model)).length} / {SUB_MODELS.length}</span>
            <button className="button small" aria-label="全选内置模型" onClick={() => setDraft(available.filter(model=>SUB_MODELS.includes(model)||draft.includes(model)))}>全选</button>
            <button className="button small" aria-label="清空内置模型" onClick={() => setDraft(draft.filter(model=>!SUB_MODELS.includes(model)))}>清空</button>
          </div>
          <div className="sub-model-settings-options">
            {available.map((model, index) => (
              <div key={model} style={{display:"contents"}}>
              {index === SUB_MODELS.length && <div className="sub-model-settings-extra sub-model-settings-actions" role="group" aria-label="额外模型选择">
                <span>额外模型 · 已选 {extras.filter(model=>draft.includes(model)).length} / {extras.length}</span>
                <button className="button small" aria-label="全选额外模型" onClick={()=>setDraft(available.filter(model=>extras.includes(model)||draft.includes(model)))}>全选</button>
                <button className="button small" aria-label="清空额外模型" onClick={()=>setDraft(draft.filter(model=>!extras.includes(model)))}>清空</button>
              </div>}
              <label key={model}>
                <input
                  type="checkbox"
                  aria-label={model}
                  checked={draft.includes(model)}
                  onChange={(event) => {
                    setDraft(
                      event.target.checked
                        ? available.filter(
                            (item) => item === model || draft.includes(item),
                          )
                        : draft.filter((item) => item !== model),
                    );
                  }}
                />
                <span>
                  {model}
                  <small>{SUB_MODELS.includes(model) ? subModelProtocolLabel(model) : "渠道额外模型"}</small>
                </span>
              </label>
              </div>
            ))}
          </div>
          </div>
          <div className="compact-dialog-footer">
            {!draft.length && <span className="dialog-footer-note" role="status">至少选择一个模型</span>}
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
