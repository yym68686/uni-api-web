import modelCatalog from "../analytics-api/model_catalog.json";

export const SUB_MODELS = modelCatalog.map(entry => entry.model);
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
const selectionCache = new Map<string, SubModel[]>();
export function loadSubModels(user: string): SubModel[] {
  try {
    const raw = localStorage.getItem(selectionKey(user));
    if (!raw) return selectionCache.get(user) || [...SUB_MODELS];
    const saved = JSON.parse(raw);
    if (Array.isArray(saved?.disabled)) {
      // Newly supported models start selected without undoing previous choices.
      const selected = SUB_MODELS.filter(
        (model) => !saved.disabled.includes(model),
      );
      if (selected.length) return selected;
    }
  } catch {
    return selectionCache.get(user) || [...SUB_MODELS];
  }
  return [...SUB_MODELS];
}
export function saveSubModels(user: string, models: SubModel[]) {
  selectionCache.set(user, [...models]);
  try {
    localStorage.setItem(
      selectionKey(user),
      JSON.stringify({
        disabled: SUB_MODELS.filter((model) => !models.includes(model)),
      }),
    );
  } catch {
    /* Keep the selection for this session when storage is unavailable. */
  }
}
