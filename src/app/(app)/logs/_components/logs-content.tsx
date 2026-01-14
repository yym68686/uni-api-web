import { LogsTableClient } from "./logs-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { LogsListResponse } from "@/lib/types";

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

const PAGE_SIZE = 50;

async function getLogs() {
  const res = await fetch(buildBackendUrl(`/logs?limit=${PAGE_SIZE}&offset=0`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isLogsListResponse(json)) return null;
  return json.items;
}

export async function LogsContent() {
  const items = (await getLogs()) ?? [];
  return <LogsTableClient initialItems={items} pageSize={PAGE_SIZE} />;
}
