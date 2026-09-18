import { ranges } from "./analytics";
export const defaultFilters = {
  keyId: "",
  sourceId: "",
  model: "",
  window: "15m",
  balanceFilter: "",
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
          (account || (view !== "sub2api" && view !== "sources")),
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

const storageKey = (base: string) => `uni-console-filters:v1:${base}`;

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
    window: field("window", ranges.map(([value]) => value)),
    balanceFilter: field("balanceFilter", ["", "low"]),
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

export function loadFilters(base: string): Filters {
  try {
    return validate(
      JSON.parse(localStorage.getItem(storageKey(base)) || "null"),
    );
  } catch {
    return { ...defaultFilters };
  }
}

export function saveFilters(base: string, filters: Filters) {
  try {
    localStorage.setItem(storageKey(base), JSON.stringify(validate(filters)));
  } catch {
    // Browsers can disable storage or exhaust its quota; filtering still works.
  }
}
