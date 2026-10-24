import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Settings2, X } from "lucide-react";
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
        <Dialog.Content className="guide-dialog sub-model-settings">
          <Dialog.Title>检测模型设置</Dialog.Title>
          <Dialog.Description>
            一键同步并检测、未筛选模型时的批量检测，仅检测这里勾选的模型。筛选单个模型后直接检测该模型。
          </Dialog.Description>
          <Dialog.Close asChild>
            <button
              className="icon-button detail-close"
              aria-label="关闭模型设置"
            >
              <X size={18} />
            </button>
          </Dialog.Close>
          <p>合计已选 {draft.filter(model => available.includes(model)).length} / {available.length}</p>
          <div className="sub-model-settings-actions" role="group" aria-label="内置模型选择">
            <span>内置模型 · 已选 {SUB_MODELS.filter(model=>draft.includes(model)).length} / {SUB_MODELS.length}</span>
            <button className="button small" aria-label="全选内置模型" onClick={() => setDraft(available.filter(model=>SUB_MODELS.includes(model)||draft.includes(model)))}>全选</button>
            <button className="button small" aria-label="清空内置模型" onClick={() => setDraft(draft.filter(model=>!SUB_MODELS.includes(model)))}>清空</button>
          </div>
          <div className="sub-model-settings-options">
            {available.map((model, index) => (
              <div key={model} style={{display:"contents"}}>
              {index === SUB_MODELS.length && <div className="sub-model-settings-extra sub-model-settings-actions" role="group" aria-label="额外模型选择">
                <span>当前筛选渠道的额外模型 · 已选 {extras.filter(model=>draft.includes(model)).length} / {extras.length}</span>
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
