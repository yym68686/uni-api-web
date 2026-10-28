import modelCatalog from "../analytics-api/model_catalog.json";

export const SUB_MODELS = modelCatalog
  .filter((entry) => !entry.model.startsWith("jev-"))
  .map((entry) => entry.model);
export type SubModel = (typeof SUB_MODELS)[number];
export const isGeminiModel = (model: string) =>
  model === "gemini-4-argon" || model === "gemini-3.1-pro" || model === "gemini-3.8-flash";
export const subModelProtocolLabel = (model: string) =>
  isGeminiModel(model)
    ? "Gemini 原生"
    : model.startsWith("claude-")
      ? "Messages"
      : "Responses";

const selectionKey = (user: string) => `uni-console-sub2api-models:v1:${user}`;
interface ModelSelection {
  schema: 2;
  known: string[];
  selected: string[];
  needsReview: boolean;
}

const selectionCache = new Map<string, ModelSelection>();

function storedSelection(user: string): ModelSelection | undefined {
  try {
    const raw = localStorage.getItem(selectionKey(user));
    const saved = raw ? JSON.parse(raw) : null;
    if (
      saved?.schema === 2 &&
      Array.isArray(saved.known) &&
      Array.isArray(saved.selected)
    ) {
      const selection: ModelSelection = {
        schema: 2,
        known: saved.known.filter((m: unknown): m is string => typeof m === "string"),
        selected: saved.selected.filter((m: unknown): m is string => typeof m === "string"),
        needsReview: saved.needsReview === true,
      };
      selectionCache.set(user, selection);
      return selection;
    }
  } catch { /* Use the session copy if storage is unavailable. */ }
  return selectionCache.get(user);
}

function persistSelection(user: string, selection: ModelSelection) {
  selectionCache.set(user, selection);
  try {
    localStorage.setItem(selectionKey(user), JSON.stringify(selection));
  } catch { /* Keep choices for this session when storage is unavailable. */ }
}

function migrateLegacySelection(user: string, disabled: string[], available: string[]): ModelSelection {
  const excluded = new Set(disabled);
  // Legacy state cannot distinguish selected extras from extras discovered
  // after the last save. Keep built-in choices and require extras to be
  // explicitly selected again rather than risk a larger paid probe scope.
  const selection: ModelSelection = {
    schema: 2,
    known: [...new Set([...SUB_MODELS, ...available])],
    selected: SUB_MODELS.filter(model => !excluded.has(model)),
    needsReview: true,
  };
  persistSelection(user, selection);
  return selection;
}

export function loadSubModels(user: string, available: string[] = SUB_MODELS): SubModel[] {
  const raw = (() => {
    try {
      const value = localStorage.getItem(selectionKey(user));
      return value ? JSON.parse(value) : null;
    } catch { return null; }
  })();
  let selection = storedSelection(user);
  if (!selection && Array.isArray(raw?.disabled)) {
    const disabled = raw.disabled.filter((m: unknown): m is string => typeof m === "string");
    selection = migrateLegacySelection(user, disabled, available);
  }
  if (!selection) return available.filter(model => SUB_MODELS.includes(model as SubModel)) as SubModel[];

  const known = new Set(selection.known);
  const selected = new Set(selection.selected);
  return available.filter(model => known.has(model) && selected.has(model)) as SubModel[];
}

export function initializeSubModels(user: string, available: string[] = SUB_MODELS) {
  // Freeze the initial default scope so later catalog growth cannot enlarge it.
  loadSubModels(user, available);
  if (!storedSelection(user)) {
    saveSubModels(user, available.filter(model => SUB_MODELS.includes(model as SubModel)) as SubModel[], available);
  }
}

export function saveSubModels(user: string, models: SubModel[], available: string[] = SUB_MODELS) {
  const previous = storedSelection(user);
  const known = new Set(previous?.known || []);
  const selected = new Set(previous?.selected || []);
  for (const model of available) {
    known.add(model);
    if (models.includes(model as SubModel)) selected.add(model);
    else selected.delete(model);
  }
  for (const model of models) {
    known.add(model);
    selected.add(model);
  }
  persistSelection(user, {
    schema: 2,
    known: [...known],
    selected: [...selected],
    needsReview: false,
  });
}

export function subModelSelectionNeedsReview(user: string): boolean {
  return storedSelection(user)?.needsReview === true;
}

// These two probes exercise Codex-specific protocols, not generic model capabilities.
export function isCapabilityCheckModel(model: string): boolean {
  return model.startsWith("gpt") || model.startsWith("codex");
}
export const capabilityCheckScope = "仅适用于 gpt 或 codex 开头的模型";
