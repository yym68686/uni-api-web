import type { ReactNode } from "react";
import type { ModelAlias } from "./ModelAliases";

// A self mapping is an ordinary selected model, even if it was serialized in
// model_mappings by a previous batch edit. Genuine aliases remain independent.
export function splitChannelModels(
  rows: { model: string; upstream_model?: string }[],
  definition: { models: string[]; model_mappings?: Record<string, string> } = {
    models: [],
  },
) {
  const originals: string[] = [],
    aliases: ModelAlias[] = [];
  for (const row of rows) {
    const upstream =
      row.upstream_model || definition.model_mappings?.[row.model] || row.model;
    if (
      upstream === row.model ||
      (definition.models.includes(row.model) &&
        (definition.model_mappings?.[row.model] || row.model) === upstream)
    ) {
      if (!originals.includes(row.model)) originals.push(row.model);
    } else
      aliases.push({
        public: row.model,
        upstream:
          definition.models.find(
            (m) => (definition.model_mappings?.[m] || m) === upstream,
          ) || upstream,
      });
  }
  return { originals, aliases };
}

export function ChannelModelSelection({
  models,
  selected,
  onChange,
  disabled,
  actions,
  status,
  canSelect,
  editing,
}: {
  models: string[];
  selected: string[];
  onChange: (models: string[]) => void;
  disabled?: boolean;
  actions?: ReactNode;
  status?: (model: string) => ReactNode;
  canSelect?: (model: string) => boolean;
  editing?: boolean;
}) {
  return (
    <fieldset disabled={disabled}>
      <legend className="model-alias-heading batch-model-heading">
        <span>模型</span>
        {actions}
      </legend>
      {editing && (
        <p className="sub-import-note">
          已接入的原模型默认勾选；可手动添加其他模型。未检测或检测失败的模型不代表上游可用，保存不会自动发起检测。
        </p>
      )}
      <div className="sub-model-options">
        {[...new Set(models)].map((model) => (
          <label key={model}>
            <input
              type="checkbox"
              checked={selected.includes(model)}
              disabled={canSelect ? !canSelect(model) : false}
              onChange={(e) =>
                onChange(
                  e.target.checked
                    ? [...selected, model]
                    : selected.filter((m) => m !== model),
                )
              }
            />
            <span>{model}</span>
            {status?.(model)}
          </label>
        ))}
      </div>
    </fieldset>
  );
}
