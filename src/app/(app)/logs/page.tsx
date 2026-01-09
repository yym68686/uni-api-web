import { Clock, ScrollText } from "lucide-react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { formatUsd } from "@/lib/format";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import type { LogsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatUtcDateTime(value: string) {
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return "—";
  return dt.toISOString().replace("T", " ").slice(0, 19);
}

function formatMs(value: number) {
  const ms = Math.max(0, Math.round(value));
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function formatTps(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value <= 0) return "0";
  if (value < 10) return value.toFixed(2);
  return value.toFixed(1);
}

function formatCostUsd(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "$0";
  if (value < 0.01) return `$${value.toFixed(6)}`;
  return formatUsd(value);
}

async function getLogs() {
  const res = await fetch(buildBackendUrl("/logs?limit=100"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isLogsListResponse(json)) return null;
  return json.items;
}

export default async function LogsPage() {
  const locale = await getRequestLocale();
  const items = (await getLogs()) ?? [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "logs.title")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "logs.subtitle")}
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ScrollText className="h-5 w-5 text-muted-foreground" />
            {t(locale, "logs.card.title")}
          </CardTitle>
          <CardDescription className="flex items-center gap-2">
            <Clock className="h-4 w-4 text-muted-foreground" />
            {t(locale, "logs.card.latest100")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              {t(locale, "logs.empty")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(locale, "logs.table.time")}</TableHead>
                  <TableHead>{t(locale, "logs.table.model")}</TableHead>
                  <TableHead>{t(locale, "logs.table.input")}</TableHead>
                  <TableHead>{t(locale, "logs.table.output")}</TableHead>
                  <TableHead>{t(locale, "logs.table.total")}</TableHead>
                  <TableHead>{t(locale, "logs.table.ttft")}</TableHead>
                  <TableHead>{t(locale, "logs.table.tps")}</TableHead>
                  <TableHead>{t(locale, "logs.table.cost")}</TableHead>
                  <TableHead>{t(locale, "logs.table.ip")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((r) => (
                  <TableRow key={r.id} className="hover:bg-muted/50">
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatUtcDateTime(r.createdAt)}
                    </TableCell>
                    <TableCell>
                      <CopyableModelId value={r.model} />
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {r.inputTokens}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {r.outputTokens}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatMs(r.totalDurationMs)}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatMs(r.ttftMs)}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatTps(r.tps)}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatCostUsd(r.costUsd)}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {r.sourceIp ?? "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
