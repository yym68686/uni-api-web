import { ApiError } from "./api";

export interface SummaryState<T> {
  version: string;
  rows: { id: string; value: T }[];
}
interface SummaryResponse<T> {
  format: "summary-v1";
  version: string;
  base_version: string;
  data: { id: string; value: T }[];
  removed: string[];
  order: string[];
}
export function mergeSummary<T>(
  previous: SummaryState<T> | undefined,
  response: SummaryResponse<T>,
): SummaryState<T> {
  if (response.base_version && previous?.version !== response.base_version)
    throw Error("数据版本已变化，请重新读取");
  const rows = new Map(
    (response.base_version ? previous?.rows || [] : []).map((row) => [
      row.id,
      row,
    ]),
  );
  for (const id of response.removed) rows.delete(id);
  for (const row of response.data) rows.set(row.id, row);
  if (
    new Set(response.order).size !== response.order.length ||
    response.order.length !== rows.size ||
    response.order.some((id) => !rows.has(id))
  )
    throw Error("列表数据不完整，请重新读取");
  return {
    version: response.version,
    rows: response.order.map((id) => rows.get(id)!),
  };
}

export async function readSummary<T, Legacy>(
  path: string,
  previous: SummaryState<T> | undefined,
  signal: AbortSignal,
  view = "summary",
): Promise<{ summary?: SummaryState<T>; legacy?: Legacy }> {
  const response = await fetch(location.origin + "/analytics" + path, {
    credentials: "same-origin",
    cache: "no-store",
    headers: {
      Accept: "application/json",
      "X-Console-View": view,
      ...(previous ? { "X-Console-Since": previous.version } : {}),
    },
    signal: AbortSignal.any([signal, AbortSignal.timeout(30_000)]),
  });
  if (response.status === 304 && previous) return { summary: previous };
  if (!response.ok)
    throw new ApiError(
      (await response.text()) || `HTTP ${response.status}`,
      response.status,
    );
  const body = await response.json();
  // During rolling upgrades an older backend can still return the full shape.
  if (body.format !== "summary-v1") return { legacy: body as Legacy };
  return { summary: mergeSummary(previous, body) };
}
