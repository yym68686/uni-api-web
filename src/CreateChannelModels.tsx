import { useState } from "react";
import type { ReactNode } from "react";
import { Plus, Search } from "lucide-react";
import { ChannelModelSelection } from "./ChannelModelSelection";
import { ModelAliases, aliasMappings } from "./ModelAliases";
import type { ModelAlias } from "./ModelAliases";
import "./createChannelModels.css";

export function CreateChannelModels({
  models,
  selected,
  aliases,
  onSelect,
  onAliases,
  onAdd,
  disabled,
  discovery,
}: {
  models: string[];
  selected: string[];
  aliases: ModelAlias[];
  onSelect: (models: string[]) => void;
  onAliases: (aliases: ModelAlias[]) => void;
  onAdd: (model: string) => void;
  disabled: boolean;
  discovery: ReactNode;
}) {
  const [search, setSearch] = useState("");
  const [manual, setManual] = useState(false);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const visible = models.filter((m) =>
    m.toLowerCase().includes(search.trim().toLowerCase()),
  );
  function add() {
    const model = name.trim();
    if (!model || /[\r\n\t]/.test(model)) {
      setError("请输入有效的模型名称");
      return;
    }
    onAdd(model);
    setName("");
    setManual(false);
    setSearch("");
    setError("");
  }
  const mapping = aliasMappings(aliases, selected);
  return (
    <section className="create-channel-models" aria-label="模型与映射">
      <div className="create-models-heading">
        <h3>
          模型与映射 <small>已选 {mapping.models.length}</small>
        </h3>
        <div className="create-models-actions">
          {discovery}
          <button
            type="button"
            className="button small"
            disabled={disabled}
            onClick={() => setManual((v) => !v)}
          >
            <Plus size={14} />
            手动添加
          </button>
        </div>
      </div>
      {manual && (
        <div className="create-model-manual">
          <input
            aria-label="手动模型名称"
            placeholder="输入上游模型名称"
            autoFocus
            value={name}
            disabled={disabled}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                add();
              }
            }}
          />
          <button
            type="button"
            className="button small"
            disabled={disabled || !name.trim()}
            onClick={add}
          >
            添加模型
          </button>
          {error && (
            <p role="alert" className="negative">
              {error}
            </p>
          )}
        </div>
      )}
      {!!models.length && (
        <div className="create-models-filter">
          <label>
            <Search size={14} />
            <input
              aria-label="搜索可选模型"
              placeholder="搜索模型…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <span>{models.length} 个模型</span>
          <button
            className="button small ghost"
            type="button"
            disabled={disabled || selected.length === models.length}
            onClick={() => onSelect(models)}
          >
            全选
          </button>
          <button
            className="button small ghost"
            type="button"
            disabled={disabled || !selected.length}
            onClick={() => onSelect([])}
          >
            清空勾选
          </button>
        </div>
      )}
      {models.length ? (
        <>
          <ChannelModelSelection
            models={visible}
            selected={selected}
            onChange={onSelect}
            disabled={disabled}
          />
          {!visible.length && <p className="muted">没有匹配的模型</p>}
        </>
      ) : (
        <p className="create-models-empty">获取上游模型，或手动添加模型名称</p>
      )}
      <ModelAliases
        models={models}
        aliases={aliases}
        onChange={onAliases}
        disabled={disabled}
      />
      {mapping.error && (
        <p role="alert" className="negative">
          {mapping.error}
        </p>
      )}
    </section>
  );
}
