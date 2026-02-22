"use client";

import * as React from "react";
import { Loader2, ScrollText } from "lucide-react";
import { toast } from "sonner";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { logsListApiPath } from "@/lib/api-paths";
import { formatUsd } from "@/lib/format";
import type { LogItem, LogsListResponse } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { useSwrLite } from "@/lib/swr-lite";

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

type BadgeVariant = React.ComponentProps<typeof Badge>["variant"];

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

function ttftVariant(value: number): "success" | "warning" | "destructive" {
  const ms = Math.max(0, Math.round(value));
  if (ms <= 5000) return "success";
  if (ms <= 10000) return "warning";
  return "destructive";
}

function statusCodeVariant(value: number): BadgeVariant {
  const code = Number.isFinite(value) ? Math.trunc(value) : 0;
  if (code >= 200 && code < 300) return "success";
  if (code >= 400 && code < 500) return "destructive";
  if (code >= 500 && code < 600) return "warning";
  if (code >= 300 && code < 400) return "secondary";
  return "outline";
}

interface LogsTableClientProps {
  initialItems: LogItem[];
  pageSize: number;
}

export function LogsTableClient({ initialItems, pageSize }: LogsTableClientProps) {
  const { locale, t } = useI18n();
  const listKey = logsListApiPath(pageSize, 0);
  const swr = useSwrLite<LogItem[]>(
    listKey,
    async (key) => {
      const res = await fetch(key, { cache: "no-store" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("common.operationFailed"))
            : t("common.operationFailed");
        throw new Error(message);
      }
      if (!isLogsListResponse(json)) throw new Error(t("common.unexpectedError"));
      return json.items;
    },
    { fallbackData: initialItems, revalidateOnFocus: false }
  );

  const baseItems = swr.data ?? initialItems;
  const [extraItems, setExtraItems] = React.useState<LogItem[]>([]);
  const [loadingMore, setLoadingMore] = React.useState(false);

  React.useEffect(() => {
    setExtraItems([]);
    setLoadingMore(false);
  }, [baseItems]);

  const items = React.useMemo(() => [...baseItems, ...extraItems], [baseItems, extraItems]);
  const offset = items.length;
  const canLoadMore = items.length > 0 && items.length % pageSize === 0;

  async function fetchPage(nextOffset: number) {
    const res = await fetch(logsListApiPath(pageSize, nextOffset), { cache: "no-store" });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const message =
        json && typeof json === "object" && "message" in json
          ? String((json as { message?: unknown }).message ?? t("common.operationFailed"))
          : t("common.operationFailed");
      throw new Error(message);
    }
    if (!isLogsListResponse(json)) throw new Error(t("common.unexpectedError"));
    return json.items;
  }

  async function loadMore() {
    if (loadingMore || !canLoadMore) return;
    setLoadingMore(true);
    try {
      const nextItems = await fetchPage(offset);
      setExtraItems((prev) => [...prev, ...nextItems]);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState
            icon={(
              <span className="inline-flex uai-float-sm">
                <ScrollText className="h-6 w-6 text-muted-foreground" />
              </span>
            )}
            title={t("logs.empty.title")}
            description={t("logs.empty.desc")}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table variant="card">
            <TableHeader>
              <TableRow>
                <TableHead>{t("logs.table.time")}</TableHead>
                <TableHead>{t("logs.table.model")}</TableHead>
                <TableHead>{t("logs.table.status")}</TableHead>
                <TableHead className="whitespace-nowrap">{t("logs.table.io")}</TableHead>
                <TableHead>{t("logs.table.total")}</TableHead>
                <TableHead>{t("logs.table.ttft")}</TableHead>
                <TableHead>{t("logs.table.tps")}</TableHead>
                <TableHead>{t("logs.table.cost")}</TableHead>
                <TableHead>{t("logs.table.ip")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.id} className="uai-cv-auto">
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    <ClientDateTime value={r.createdAt} locale={locale} timeStyle="medium" />
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    <CopyableModelId value={r.model} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    <Badge
                      variant={statusCodeVariant(r.statusCode)}
                      className="rounded-full px-2 py-0 text-[10px] font-mono tabular-nums"
                    >
                      {r.statusCode}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {r.inputTokens} / {r.cachedTokens} / {r.outputTokens}
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {formatMs(r.totalDurationMs)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    <Badge
                      variant={ttftVariant(r.ttftMs)}
                      className="rounded-full px-2 py-0 text-[10px] font-mono tabular-nums"
                    >
                      {formatMs(r.ttftMs)}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {formatTps(r.tps)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {formatCostUsd(r.costUsd)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {r.sourceIp ?? "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="flex justify-center py-4">
            <Button
              type="button"
              variant="outline"
              className="rounded-xl bg-transparent"
              disabled={loadingMore || !canLoadMore}
              onClick={() => void loadMore()}
            >
              {loadingMore ? (
                <span className="inline-flex animate-spin">
                  <Loader2 className="h-4 w-4" />
                </span>
              ) : null}
              {loadingMore ? t("common.loadingMore") : t("common.loadMore")}
            </Button>
          </div>
        </CardContent>
      )}
    </Card>
  );
}
