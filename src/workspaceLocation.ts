import { consoleView, defaultFilters, validateFilters, type Filters, type View } from "./preferences";

// URL state lives in the fragment so the CDN and server never receive it.
// Free-text search and credentials are deliberately excluded.
const shared: (keyof Filters)[] = [
  "sourceId", "keyId", "model", "window", "statusFilter", "failureReason",
  "endpoint", "stream", "balanceFilter", "balanceTopN", "balanceThreshold",
];
const fields = (view: View) => view === "requests" ? [...shared, "sort" as const] : shared;
export const hasChannelScope = (view: View): view is "channels" | "routing" | "balances" | "overview" =>
  view === "channels" || view === "routing" || view === "balances" || view === "overview";

export type LogPosition = { id: string; source: string; instance?: string };
export type LogWindow = { from: number; to: number };
export function readWorkspace(hash: string, account: boolean) {
  const params = new URLSearchParams(hash.slice(1));
  const view = consoleView(params.get("view"), account);
  const filters = view && (hasChannelScope(view) || view === "requests") && params.has("scope")
    ? validateFilters(Object.fromEntries(fields(view).map(key => [key, params.get(key)?.slice(0, 512)])))
    : undefined;
  const from = Number(params.get("from")), to = Number(params.get("to"));
  const id = params.get("request")?.slice(0, 512);
  return { view, filters,
    detail: view === "channels" || view === "routing" || view === "balances" ? params.get("channel")?.slice(0, 1400) || null : null,
    request: view === "requests" && id ? {id, source:params.get("source")?.slice(0,512) || "", instance:params.get("instance")?.slice(0,512)} : null,
    logWindow: view === "requests" && validLogWindow(from,to) ? {from,to} : null,
  };
}

export function workspaceHash(view: View, filters?: Filters, detail?: string | null) {
  const params = new URLSearchParams({view});
  if (filters && (hasChannelScope(view) || view === "requests")) {
    params.set("scope", "1");
    for (const key of fields(view)) if (filters[key] !== defaultFilters[key]) params.set(key, filters[key]);
  }
  if (detail && (view === "channels" || view === "routing" || view === "balances")) params.set("channel", detail);
  return "#" + params;
}

export const validLogWindow = (from: number, to: number) =>
  Number.isSafeInteger(from) && Number.isSafeInteger(to) && from > 0 && to > from &&
  to - from <= 366 * 86_400_000 && to <= Date.now() + 300_000;

export function requestLocation(filters: Filters, request: LogPosition | null, window: LogWindow | null) {
  const params = new URLSearchParams(workspaceHash("requests", filters).slice(1));
  if (request) {
    params.set("request", request.id);
    if (request.source) params.set("source", request.source);
    if (request.instance != null) params.set("instance", request.instance);
  }
  if (window) { params.set("from", String(window.from)); params.set("to", String(window.to)); }
  return "#" + params;
}
