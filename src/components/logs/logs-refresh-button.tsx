"use client";

import * as React from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { LogItem, LogsListResponse } from "@/lib/types";
import { dispatchUiEvent, UI_EVENTS } from "@/lib/ui-events";

function isLogsListResponse(value: unknown): value is LogsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

interface LogsRefreshButtonProps {
  pageSize: number;
  className?: string;
}

export function LogsRefreshButton({ pageSize, className }: LogsRefreshButtonProps) {
  const { t } = useI18n();
  const [loading, setLoading] = React.useState(false);

  async function refresh() {
    if (loading) return;
    setLoading(true);
    try {
      const res = await fetch(`/api/logs?limit=${encodeURIComponent(pageSize)}&offset=0`, { cache: "no-store" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("common.operationFailed"))
            : t("common.operationFailed");
        throw new Error(message);
      }
      if (!isLogsListResponse(json)) throw new Error(t("common.unexpectedError"));
      dispatchUiEvent<LogItem[]>(UI_EVENTS.logsRefreshed, json.items);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <Button
      type="button"
      variant="outline"
      className={className}
      disabled={loading}
      onClick={() => void refresh()}
    >
      <RefreshCw className={loading ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
      {loading ? t("common.refreshing") : t("common.refresh")}
    </Button>
  );
}

