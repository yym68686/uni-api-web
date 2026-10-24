export const subFilterDefaults = {
  search: "",
  accountId: "",
  model: "",
  maxRate: "",
  sort: "asc",
  availability: "",
  groupStatus: "available",
  priceStatus: "",
  compaction: "",
  toolUse: "",
  quality: "",
  minQuality: "",
  platform: "",
  joined: "",
  modelMatch: "",
  fingerprint: "",
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
    joined: field("joined", ["", "joined", "unjoined"]),
    groupStatus: field("groupStatus", ["available", "all", "unavailable"]) || subFilterDefaults.groupStatus,
    fingerprint: field("fingerprint", ["", "match", "mismatch", "insufficient", "error", "queued", "running", "interrupted", "untested", "not_applicable"]),
    modelMatch: field("modelMatch", ["", "match", "mismatch", "missing", "invalid", "unavailable", "legacy", "untested"]),
    platform: field("platform"),
    search: field("search"),
    accountId: field("accountId"),
    model: field("model").slice(0, 512),
    maxRate:
      maxRate === "mismatch" ||
      (maxRate !== "" && Number.isFinite(Number(maxRate)) && Number(maxRate) >= 0)
        ? maxRate
        : "",
    sort: typeof v.sort === "string" && ["", "asc", "desc"].includes(v.sort)
      ? v.sort : subFilterDefaults.sort,
    availability: field("availability", ["", "success", "error", "untested"]),
    priceStatus: field("priceStatus", ["", "normal", "abnormal", "unconfirmed", "unconfirmed_match", "unconfirmed_mismatch"]),
    compaction: field("compaction", ["", "supported", "unsupported", "error", "untested", "not_applicable"]),
    toolUse: field("toolUse", ["", "supported", "unsupported", "error", "untested", "not_applicable"]),
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
