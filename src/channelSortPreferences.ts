import {
  activeChannelSortRules,
  parseChannelSortRules,
} from "./channelSorting";
import type { ChannelSortRule } from "./channelSorting";
import type { Filters } from "./preferences";

export interface SortTemplate {
  id: string;
  name: string;
  rules: ChannelSortRule[];
}
export interface ScopedSort {
  sort: string;
  rules: ChannelSortRule[];
  templateId?: string;
}
export interface ChannelSortPreferences {
  scopes: Record<string, ScopedSort>;
  templates: SortTemplate[];
  modelTemplates: Record<string, string>;
}
export function channelSortScope(filters: Filters) {
  return JSON.stringify([
    filters.model,
    filters.sourceId,
    filters.keyId,
    filters.window,
    filters.endpoint,
    filters.stream,
    filters.statusFilter,
    filters.balanceFilter,
    filters.search.toLowerCase(),
  ]);
}
export const defaultChannelSort = (): ScopedSort => ({
  sort: "config",
  rules: [],
});
const key = (base: string) => `uni-console-channel-sorts:v1:${base}`;
const cleanRules = (value: unknown) =>
  parseChannelSortRules(JSON.stringify(value));
const object = (value: unknown): Record<string, unknown> =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
function validate(value: unknown): ChannelSortPreferences {
  const raw = object(value);
  const templates: SortTemplate[] = [];
  const ids = new Set<string>();
  for (const item of Array.isArray(raw.templates) ? raw.templates : []) {
    const t = object(item);
    if (
      typeof t.id !== "string" ||
      !t.id ||
      ids.has(t.id) ||
      typeof t.name !== "string" ||
      !t.name.trim()
    )
      continue;
    ids.add(t.id);
    templates.push({
      id: t.id,
      name: t.name.trim(),
      rules: cleanRules(t.rules),
    });
  }
  const scopes = Object.fromEntries(
    Object.entries(object(raw.scopes)).flatMap(([scope, value]) => {
      try {
        const parsed = JSON.parse(scope);
        if (
          !Array.isArray(parsed) ||
          parsed.length !== 9 ||
          !parsed.every((v) => typeof v === "string")
        )
          return [];
      } catch {
        return [];
      }
      const saved = object(value);
      if (
        !["config", "custom", "success", "latency", "wait"].includes(
          String(saved.sort),
        )
      )
        return [];
      return [
        [
          scope,
          {
            sort: saved.sort as string,
            rules: cleanRules(saved.rules),
            ...(typeof saved.templateId === "string" &&
            ids.has(saved.templateId)
              ? { templateId: saved.templateId }
              : {}),
          },
        ],
      ];
    }),
  );
  const modelTemplates = Object.fromEntries(
    Object.entries(object(raw.modelTemplates)).filter(
      ([, id]) => typeof id === "string" && ids.has(id),
    ),
  ) as Record<string, string>;
  return { scopes, templates, modelTemplates };
}
export function loadChannelSortPreferences(
  base: string,
  legacy: Filters,
): ChannelSortPreferences {
  try {
    const raw = localStorage.getItem(key(base));
    if (raw !== null) return validate(JSON.parse(raw));
  } catch {
    /* Keep the last visible scope usable if storage cannot be read. */
  }
  // Migrate the shared setting only into the currently saved filter scope.
  // Never use it as a default for other models or "all models".
  const rules = activeChannelSortRules(legacy.sort, legacy.sortRules);
  return {
    scopes: {
      [channelSortScope(legacy)]: {
        sort: rules.length ? legacy.sort : "config",
        rules,
      },
    },
    templates: [],
    modelTemplates: {},
  };
}
export function saveChannelSortPreferences(
  base: string,
  value: ChannelSortPreferences,
) {
  try {
    localStorage.setItem(key(base), JSON.stringify(validate(value)));
  } catch {
    /* In-memory editing remains available. */
  }
}
export function scopedChannelSort(
  value: ChannelSortPreferences,
  scope: string,
): ScopedSort {
  return Object.hasOwn(value.scopes, scope)
    ? value.scopes[scope]
    : defaultChannelSort();
}
export function applyScopedChannelSort(
  value: ChannelSortPreferences,
  scope: string,
  model: string,
  selection: ScopedSort,
): ChannelSortPreferences {
  const template = value.templates.find((t) => t.id === selection.templateId);
  return {
    ...value,
    scopes: {
      ...value.scopes,
      [scope]: {
        ...selection,
        rules: selection.rules.map((r) => ({ ...r })),
        templateId: template?.id,
      },
    },
    modelTemplates: template
      ? { ...value.modelTemplates, [model]: template.id }
      : value.modelTemplates,
  };
}
export function deleteSortTemplate(
  value: ChannelSortPreferences,
  id: string,
): ChannelSortPreferences {
  return {
    ...value,
    templates: value.templates.filter((t) => t.id !== id),
    modelTemplates: Object.fromEntries(
      Object.entries(value.modelTemplates).filter(
        ([, template]) => template !== id,
      ),
    ),
    scopes: Object.fromEntries(
      Object.entries(value.scopes).map(([scope, sort]) => [
        scope,
        sort.templateId === id ? { sort: sort.sort, rules: sort.rules } : sort,
      ]),
    ),
  };
}
