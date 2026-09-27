import { ranges } from "./analytics";
export const defaultFilters = {
  keyId: "",
  sourceId: "",
  model: "",
  window: "15m",
  balanceFilter: "",
  balanceTopN: "",
  balanceThreshold: "",
  statusFilter: "",
  search: "",
  sort: "config",
  endpoint: "all",
  stream: "all",
};

export type Filters = typeof defaultFilters;

const views = [
  "sub2api",
  "channels",
  "balances",
  "overview",
  "prices",
  "sources",
  "automations",
] as const;
export type View = (typeof views)[number];
const viewStorageKey = (base: string) => `uni-console-view:v1:${base}`;

export function loadView(base: string, account: boolean): View {
  try {
    const saved = localStorage.getItem(viewStorageKey(base));
    return (
      views.find(
        (view) =>
          view === saved &&
          (account ||
            (view !== "sub2api" &&
              view !== "sources" &&
              view !== "automations")),
      ) || "channels"
    );
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

export type FilterScope = "channels" | "balances";
const storageKey = (base: string, scope: FilterScope) =>
  `uni-console-filters:v2:${scope}:${base}`;
const legacyStorageKey = (base: string) => `uni-console-filters:v1:${base}`;

function validate(value: unknown): Filters {
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
      return Number.isFinite(amount) && amount >= 0 && amount % 10 === 0
        ? value
        : "";
    })(),
    statusFilter: field("statusFilter", ["", "eligible", "unavailable"]),
    search: field("search"),
    sort: field("sort", ["config", "success", "latency", "wait"]),
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
      localStorage.getItem(legacyStorageKey(base));
    return validate(JSON.parse(saved || "null"));
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
      JSON.stringify(validate(filters)),
    );
  } catch {
    // Browsers can disable storage or exhaust its quota; filtering still works.
  }
}
