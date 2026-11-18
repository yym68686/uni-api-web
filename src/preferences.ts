import { ranges } from "./analytics";
import { parseChannelSortRules } from "./channelSorting";
export const defaultFilters = {
  keyId: "",
  sourceId: "",
  model: "",
  window: "15m",
  balanceFilter: "",
  balanceTopN: "",
  balanceThreshold: "",
  statusFilter: "",
  failureReason: "",
  search: "",
  sort: "config",
  sortRules: "",
  endpoint: "all",
  stream: "all",
};

export type Filters = typeof defaultFilters;

const views = [
  "sub2api",
  "routing",
  "channels",
  "balances",
  "overview",
  "prices",
  "sources",
  "automations",
  "requests",
] as const;
export type View = (typeof views)[number];
const viewStorageKey = (base: string) => `uni-console-view:v1:${base}`;
export const consoleView = (value: string | null, account: boolean) =>
  views.find(view => view === value && (account || !["sub2api","routing","sources","automations"].includes(view)));

export function loadView(base: string, account: boolean): View {
  try {
    const saved = localStorage.getItem(viewStorageKey(base));
    return consoleView(saved, account) || "channels";
  } catch {
    return "channels";
  }
}

export function saveView(base: string, view: View) {
  try {
    localStorage.setItem(viewStorageKey(base), view);
  } catch {
    // Navigation still works when browser storage is unavailable.
  }
}

export type FilterScope = "channels" | "routing" | "balances" | "requests" | "overview";
const storageKey = (base: string, scope: FilterScope) =>
  `uni-console-filters:v2:${scope}:${base}`;
const legacyStorageKey = (base: string) => `uni-console-filters:v1:${base}`;

export function validateFilters(value: unknown): Filters {
  const saved = value && typeof value === "object" ? value : {};
  const field = (name: keyof Filters, options?: string[]) => {
    const value = (saved as Record<string, unknown>)[name];
    return typeof value === "string" && (!options || options.includes(value))
      ? value
      : defaultFilters[name];
  };
  // Only persist the filter key's opaque ID, never the connection credential.
  return {
    keyId: field("keyId"),
    sourceId: field("sourceId"),
    model: field("model"),
    window: field(
      "window",
      ranges.map(([value]) => value),
    ),
    balanceFilter: field("balanceFilter", ["", "low"]),
    balanceTopN: field("balanceTopN", [
      "",
      ...Array.from({ length: 10 }, (_, i) => String(i + 1)),
      "20",
      "50",
      "100",
    ]),
    balanceThreshold: (() => {
      const value = field("balanceThreshold");
      if (!value) return "";
      const amount = Number(value);
      return Number.isFinite(amount) && amount >= 0
        ? value
        : "";
    })(),
    statusFilter: field("statusFilter", [
      "",
      "eligible",
      "unavailable",
      "success",
      "failed",
      "cancelled",
      "unknown",
    ]),
    failureReason: (() => {
      const reason = field("failureReason");
      return /^[a-z][a-z0-9_]{0,79}$/.test(reason) ? reason : "";
    })(),
    search: field("search"),
    sort: field("sort", ["config", "success", "latency", "wait", "latency-desc", "wait-desc", "oldest", "custom", "created", "created-desc", "text", "text-desc", "duration", "duration-desc"]),
    sortRules: (() => {
      const rules = parseChannelSortRules(field("sortRules"));
      return rules.length ? JSON.stringify(rules) : "";
    })(),
    endpoint:
      field("endpoint") === "all" ||
      /^\/[^?#\s]{1,500}$/.test(field("endpoint"))
        ? field("endpoint")
        : "all",
    stream: field("stream", ["all", "true", "false"]),
  };
}

export function loadFilters(
  base: string,
  scope: FilterScope = "channels",
): Filters {
  try {
    // Keep existing preferences on upgrade. Each page saves its own snapshot
    // from then on, including an explicit reset to defaults.
    const saved =
      localStorage.getItem(storageKey(base, scope)) ??
      (scope === "requests" || scope === "overview"
        ? null
        : localStorage.getItem(legacyStorageKey(base)));
    return validateFilters(JSON.parse(saved || "null"));
  } catch {
    return { ...defaultFilters };
  }
}

export function saveFilters(
  base: string,
  filters: Filters,
  scope: FilterScope = "channels",
) {
  try {
    localStorage.setItem(
      storageKey(base, scope),
      JSON.stringify(validateFilters(filters)),
    );
  } catch {
    // Browsers can disable storage or exhaust its quota; filtering still works.
  }
}
