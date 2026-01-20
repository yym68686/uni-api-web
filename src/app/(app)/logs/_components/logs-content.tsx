import { LogsTableClient } from "./logs-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { LogItem, LogsListResponse } from "@/lib/types";

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

export const LOGS_PAGE_SIZE = 50;

export async function getLogs() {
  const res = await fetch(buildBackendUrl(`/logs?limit=${LOGS_PAGE_SIZE}&offset=0`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isLogsListResponse(json)) return null;
  return json.items;
}

interface LogsContentProps {
  initialItemsPromise?: Promise<LogItem[] | null>;
}

export async function LogsContent({ initialItemsPromise }: LogsContentProps) {
  const items = (await (initialItemsPromise ?? getLogs())) ?? [];
  return <LogsTableClient initialItems={items} pageSize={LOGS_PAGE_SIZE} />;
}
