"use client";

import * as React from "react";
import { Loader2, ScrollText } from "lucide-react";
import { toast } from "sonner";

import { useDisplayCurrency } from "@/components/currency/currency-provider";
import { CopyableModelId } from "@/components/models/copyable-model-id";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { logsListApiPath } from "@/lib/api-paths";
import { convertUsdToDisplay, formatDisplayCurrency, type DisplayCurrency } from "@/lib/currency";
import type { LogItem, LogsCursor, LogsListResponse } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { mutateSwrLite, useSwrLite } from "@/lib/swr-lite";

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

interface DisplayCurrencySnapshot {
  currency: DisplayCurrency;
  cnyPerUsd: number;
}

function formatCostUsd(value: number, locale: string, displayCurrency: DisplayCurrencySnapshot) {
  if (!Number.isFinite(value) || value <= 0) {
    return formatDisplayCurrency(0, {
      locale,
      maximumFractionDigits: 0,
      ...displayCurrency
    });
  }
  const displayValue = convertUsdToDisplay(value, displayCurrency.currency, displayCurrency.cnyPerUsd);
  const maxDigits = displayValue < 0.01 ? 6 : 4;
  return formatDisplayCurrency(value, {
    locale,
    maximumFractionDigits: maxDigits,
    ...displayCurrency
  });
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

function compareLogItemsDesc(a: LogItem, b: LogItem) {
  const aTime = Date.parse(a.createdAt);
  const bTime = Date.parse(b.createdAt);
  if (Number.isFinite(aTime) && Number.isFinite(bTime) && aTime !== bTime) {
    return bTime - aTime;
  }
  const createdAtOrder = b.createdAt.localeCompare(a.createdAt);
  if (createdAtOrder !== 0) return createdAtOrder;
  return b.id.localeCompare(a.id);
}

function mergeLogItems(baseItems: LogItem[], extraItems: LogItem[]) {
  const byId = new Map<string, LogItem>();
  for (const item of [...baseItems, ...extraItems]) {
    if (!byId.has(item.id)) byId.set(item.id, item);
  }
  return [...byId.values()].sort(compareLogItemsDesc);
}

interface LogsTableClientProps {
  initialResponse: LogsListResponse;
  pageSize: number;
}

export function LogsTableClient({ initialResponse, pageSize }: LogsTableClientProps) {
  const { locale, t } = useI18n();
  const displayCurrency = useDisplayCurrency();
  const listKey = logsListApiPath(pageSize, { offset: 0 });
  const swr = useSwrLite<LogsListResponse>(
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
      return json;
    },
    { fallbackData: initialResponse, revalidateOnFocus: false }
  );

  React.useEffect(() => {
    void mutateSwrLite(listKey, initialResponse);
  }, [initialResponse, listKey]);

  const baseResponse = swr.data ?? initialResponse;
  const baseItems = baseResponse.items;
  const [extraItems, setExtraItems] = React.useState<LogItem[]>([]);
  const [nextCursor, setNextCursor] = React.useState<LogsCursor | null>(baseResponse.nextCursor ?? null);
  const [loadingMore, setLoadingMore] = React.useState(false);

  React.useEffect(() => {
    setExtraItems([]);
    setNextCursor(baseResponse.nextCursor ?? null);
    setLoadingMore(false);
  }, [baseResponse]);

  const items = React.useMemo(() => mergeLogItems(baseItems, extraItems), [baseItems, extraItems]);
  const canLoadMore = nextCursor !== null;

  async function fetchPage(cursor: LogsCursor) {
    const res = await fetch(
      logsListApiPath(pageSize, {
        before: cursor.createdAt,
        beforeId: cursor.id
      }),
      { cache: "no-store" }
    );
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const message =
        json && typeof json === "object" && "message" in json
          ? String((json as { message?: unknown }).message ?? t("common.operationFailed"))
          : t("common.operationFailed");
      throw new Error(message);
    }
    if (!isLogsListResponse(json)) throw new Error(t("common.unexpectedError"));
    return json;
  }

  async function loadMore() {
    if (loadingMore || !nextCursor) return;
    setLoadingMore(true);
    try {
      const nextPage = await fetchPage(nextCursor);
      setExtraItems((prev) => [...prev, ...nextPage.items]);
      setNextCursor(nextPage.nextCursor ?? null);
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
                <TableHead>{t("logs.table.endpoint")}</TableHead>
                <TableHead>{t("logs.table.streaming")}</TableHead>
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
                    {formatCostUsd(r.costUsd, locale, displayCurrency)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-mono tabular-nums text-xs text-muted-foreground">
                    {r.sourceIp ?? "—"}
                  </TableCell>
                  <TableCell
                    className="max-w-[220px] truncate whitespace-nowrap font-mono text-xs text-muted-foreground"
                    title={r.requestEndpoint ?? undefined}
                  >
                    {r.requestEndpoint ?? "—"}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    <Badge
                      variant={r.isStreaming ? "success" : "secondary"}
                      className="rounded-full px-2 py-0 text-[10px] font-mono"
                    >
                      {r.isStreaming ? t("logs.stream.yes") : t("logs.stream.no")}
                    </Badge>
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
