import { NextResponse } from "next/server";

import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { LogItem, LogsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

const EXPORT_PAGE_SIZE = 200;
const CSV_HEADERS = [
  "created_at",
  "id",
  "model",
  "ok",
  "status_code",
  "input_tokens",
  "cached_tokens",
  "output_tokens",
  "total_duration_ms",
  "ttft_ms",
  "tps",
  "cost_usd",
  "source_ip"
] as const;

class RouteError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

interface FastApiErrorBody {
  detail?: unknown;
}

async function readBackendMessage(res: Response) {
  const contentType = res.headers.get("content-type") ?? "";

  if (contentType.includes("application/json")) {
    const json: unknown = await res.json().catch(() => null);
    const detail =
      json && typeof json === "object" && "detail" in json
        ? (json as FastApiErrorBody).detail
        : null;

    if (typeof detail === "string" && detail.trim().length > 0) {
      return detail;
    }

    if (Array.isArray(detail)) {
      const first = detail[0];
      if (first && typeof first === "object" && "msg" in first) {
        const message = (first as { msg?: unknown }).msg;
        if (typeof message === "string" && message.trim().length > 0) {
          return message;
        }
      }
    }
  }

  const text = await res.text().catch(() => "");
  if (text.trim().length > 0) return text.trim();
  if (res.status === 401) return "Unauthorized";
  return "Upstream error";
}

function escapeCsv(value: string | number | boolean | null | undefined) {
  const raw = value == null ? "" : String(value);
  if (!/[",\r\n]/.test(raw)) return raw;
  return `"${raw.replace(/"/g, "\"\"")}"`;
}

function toCsvRow(item: LogItem) {
  return [
    item.createdAt,
    item.id,
    item.model,
    item.ok,
    item.statusCode,
    item.inputTokens,
    item.cachedTokens,
    item.outputTokens,
    item.totalDurationMs,
    item.ttftMs,
    item.tps,
    item.costUsd,
    item.sourceIp
  ]
    .map(escapeCsv)
    .join(",");
}

function buildCsv(items: LogItem[]) {
  const rows = items.map(toCsvRow);
  return `\uFEFF${CSV_HEADERS.join(",")}\r\n${rows.join("\r\n")}`;
}

function buildFilename(now = new Date()) {
  const stamp = now.toISOString().replace(/\.\d{3}Z$/, "Z").replace(/:/g, "-");
  return `logs-${stamp}.csv`;
}

async function fetchLogsPage(offset: number) {
  const res = await fetch(buildBackendUrl(`/logs?limit=${EXPORT_PAGE_SIZE}&offset=${offset}`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });

  if (!res.ok) {
    throw new RouteError(res.status, await readBackendMessage(res));
  }

  const json: unknown = await res.json().catch(() => null);
  if (!isLogsListResponse(json)) {
    throw new RouteError(502, "Unexpected response shape");
  }

  return json.items;
}

async function fetchAllLogs() {
  const items: LogItem[] = [];
  let offset = 0;

  while (true) {
    const pageItems = await fetchLogsPage(offset);
    items.push(...pageItems);

    if (pageItems.length < EXPORT_PAGE_SIZE) break;
    offset += pageItems.length;
  }

  return items;
}

export async function GET() {
  try {
    const items = await fetchAllLogs();
    const csv = buildCsv(items);

    return new Response(csv, {
      status: 200,
      headers: {
        "cache-control": "no-store",
        "content-disposition": `attachment; filename="${buildFilename()}"`,
        "content-type": "text/csv; charset=utf-8",
        "x-log-export-count": String(items.length)
      }
    });
  } catch (err) {
    const status = err instanceof RouteError ? err.status : 500;
    const message = err instanceof Error ? err.message : "Operation failed";
    return NextResponse.json({ message }, { status });
  }
}
