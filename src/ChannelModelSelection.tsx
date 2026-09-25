import type { ReactNode } from "react";
import type { ModelAlias } from "./ModelAliases";
import type { SubModelCheck } from "./sub2apiResults";

export const modelIsAvailable = (check?: SubModelCheck) =>
  check?.state === "done" && check.result?.availability.status === "success";

// Existing routes may be retained verbatim even after a failed retest. Only
// new public/upstream pairs require a successful availability check.
export function unavailableModelChanges(
  desired: Record<string, string>,
  saved: Record<string, string>,
  available: (model: string) => boolean,
) {
  return Object.entries(desired)
    .filter(
      ([name, upstream]) => saved[name] !== upstream && !available(upstream),
    )
    .map(([name]) => name);
}

// Failed/unknown pairs that are already serving may be retained in place. A
// batch edit must not turn that retention exception into permission to add
// the pair to every other caller key.
export function retainedOnlyModels(
  desired: Record<string, string>,
  saved: Record<string, string>,
  available: (model: string) => boolean,
) {
  return Object.fromEntries(
    Object.entries(desired).filter(
      ([name, upstream]) => saved[name] === upstream && !available(upstream),
    ),
  );
}

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
          已接入的原模型默认勾选，可取消；新增勾选或重命名仅限检测可用的模型。检测失败、未检测或检测中的模型需检测通过后再添加。
        </p>
      )}
      <div className="sub-model-options">
        {[...new Set(models)].map((model) => (
          <label key={model}>
            <input
              type="checkbox"
              checked={selected.includes(model)}
              aria-label={model}
              disabled={
                !selected.includes(model) && !!canSelect && !canSelect(model)
              }
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
