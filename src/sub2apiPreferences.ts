import { SUB_MODELS } from "./sub2apiModels";
export const subFilterDefaults = {
  search: "",
  accountId: "",
  model: "",
  maxRate: "",
  sort: "",
  availability: "",
  priceStatus: "",
  compaction: "",
  toolUse: "",
  quality: "",
  minQuality: "",
  platform: "",
};
export type SubFilters = typeof subFilterDefaults;
const cache = new Map<string, SubFilters>();
const key = (user: string) => `uni-console-sub2api-filters:v1:${user}`;
function validate(raw: unknown): SubFilters {
  const v =
    raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
  const field = (name: keyof SubFilters, allowed?: readonly string[]) =>
    typeof v[name] === "string" &&
    (!allowed || allowed.includes(v[name] as string))
      ? (v[name] as string)
      : "";
  const maxRate = field("maxRate");
  return {
    platform: field("platform"),
    search: field("search"),
    accountId: field("accountId"),
    model: field("model", ["", ...SUB_MODELS]),
    maxRate:
      maxRate !== "" && Number.isFinite(Number(maxRate)) && Number(maxRate) >= 0
        ? maxRate
        : "",
    sort: field("sort", ["", "asc", "desc"]),
    availability: field("availability", ["", "success", "error", "untested"]),
    priceStatus: field("priceStatus", ["", "normal", "abnormal", "unconfirmed"]),
    compaction: field("compaction", ["", "supported", "unsupported", "error", "untested"]),
    toolUse: field("toolUse", ["", "supported", "unsupported", "error", "untested"]),
    quality: field("quality", ["", "pass", "fail", "inconclusive", "error"]),
    minQuality: field("minQuality", ["", ...Array.from({ length: 11 }, (_, i) => String(i * 10))]),
  };
}
export function loadSubFilters(user: string): SubFilters {
  try {
    const raw = localStorage.getItem(key(user));
    return raw ? validate(JSON.parse(raw)) : { ...subFilterDefaults };
  } catch {}
  return cache.get(user) || { ...subFilterDefaults };
}
export function saveSubFilters(user: string, filters: SubFilters) {
  const saved = validate(filters);
  cache.set(user, saved);
  try {
    localStorage.setItem(key(user), JSON.stringify(saved));
  } catch {}
}
