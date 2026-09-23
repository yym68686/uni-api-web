import modelCatalog from "../analytics-api/model_catalog.json";

export const SUB_MODELS = modelCatalog
  .filter((entry) => !entry.model.startsWith("jev-"))
  .map((entry) => entry.model);
export type SubModel = (typeof SUB_MODELS)[number];
export const isGeminiModel = (model: string) =>
  model === "gemini-3.1-pro" || model === "gemini-3.8-flash";
export const subModelProtocolLabel = (model: string) =>
  isGeminiModel(model)
    ? "Gemini 原生"
    : model.startsWith("claude-")
      ? "Messages"
      : "Responses";

const selectionKey = (user: string) => `uni-console-sub2api-models:v1:${user}`;
const selectionCache = new Map<string, string[]>();
function disabledModels(user: string): string[] {
  try {
    const raw = localStorage.getItem(selectionKey(user));
    const saved = raw ? JSON.parse(raw) : null;
    if (Array.isArray(saved?.disabled)) return saved.disabled.filter((m: unknown): m is string => typeof m === "string");
  } catch { /* Use the session copy if storage is unavailable. */ }
  return selectionCache.get(user) || [];
}
export function loadSubModels(user: string, available: string[] = SUB_MODELS): SubModel[] {
  const disabled = new Set(disabledModels(user));
  return available.filter(model => !disabled.has(model));
}
export function saveSubModels(user: string, models: SubModel[], available: string[] = SUB_MODELS) {
  // Changing filters must not reset selections for models outside this view.
  const disabled = [...new Set([
    ...disabledModels(user).filter(model => !available.includes(model)),
    ...available.filter(model => !models.includes(model)),
  ])];
  selectionCache.set(user, disabled);
  try {
    localStorage.setItem(selectionKey(user), JSON.stringify({ disabled }));
  } catch { /* Keep choices for this session when storage is unavailable. */ }
}
