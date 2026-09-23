import { Plus, Trash2 } from "lucide-react";
import type { ReactNode } from "react";

export interface ModelAlias {
  upstream: string;
  public: string;
}
export function aliasMappings(aliases: ModelAlias[], originals: string[]) {
  const mappings: Record<string, string> = {};
  let error = "";
  const names = new Set(originals);
  for (const alias of aliases) {
    const name = alias.public.trim();
    if (!name || !alias.upstream) {
      error = "请填写完整的原来的名字和重命名后的名字。";
      break;
    }
    if (names.has(name)) {
      error = `对外模型名 ${name} 重复，请取消勾选同名原模型或更换名称。`;
      break;
    }
    if (name.length > 256 || /[\r\n\t]/.test(name)) {
      error = "对外模型名格式无效。";
      break;
    }
    names.add(name);
    mappings[name] = alias.upstream;
  }
  return { mappings, error, models: [...originals, ...Object.keys(mappings)] };
}
export function ModelAliases({
  models,
  aliases,
  onChange,
  disabled = false,
  actions,
}: {
  models: string[];
  aliases: ModelAlias[];
  onChange: (aliases: ModelAlias[]) => void;
  disabled?: boolean;
  actions?: ReactNode;
}) {
  return (
    <section className="model-aliases">
      <div className="model-alias-heading">
        <h4>模型重命名</h4>
        <div className="model-section-actions">
          {actions}
          <button
            type="button"
            className="button small"
            disabled={disabled || !models.length}
            onClick={() =>
              onChange([...aliases, { upstream: models[0], public: "" }])
            }
          >
            <Plus size={13} />
            添加重命名
          </button>
        </div>
      </div>
      <p className="muted">
        对外模型名用于客户端请求和模型筛选。原模型仍可在上方单独勾选。
      </p>
      {aliases.map((alias, index) => (
        <div className="model-alias-row" key={index}>
          <label>
            原来的名字
            <select
              aria-label={`重命名 ${index + 1} 上游模型`}
              disabled={disabled}
              value={alias.upstream}
              onChange={(e) =>
                onChange(
                  aliases.map((a, i) =>
                    i === index ? { ...a, upstream: e.target.value } : a,
                  ),
                )
              }
            >
              {models.map((m) => (
                <option key={m}>{m}</option>
              ))}
            </select>
          </label>
          <span aria-hidden="true">→</span>
          <label>
            重命名后的名字
            <input
              aria-label={`重命名 ${index + 1} 对外模型名`}
              disabled={disabled}
              value={alias.public}
              placeholder="例如 gpt-5.6-luna"
              onChange={(e) =>
                onChange(
                  aliases.map((a, i) =>
                    i === index ? { ...a, public: e.target.value } : a,
                  ),
                )
              }
            />
          </label>
          <button
            type="button"
            className="icon-button"
            aria-label={`删除重命名 ${index + 1}`}
            disabled={disabled}
            onClick={() => onChange(aliases.filter((_, i) => i !== index))}
          >
            <Trash2 size={15} />
          </button>
        </div>
      ))}
    </section>
  );
}
