"use client";

import * as React from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import { logsListApiPath } from "@/lib/api-paths";
import type { LogsListResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
import { mutateSwrLite } from "@/lib/swr-lite";

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
      const key = logsListApiPath(pageSize, 0);
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
      await mutateSwrLite(key, json.items);
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
      <span className={cn("inline-flex", loading ? "animate-spin" : null)}>
        <RefreshCw className="h-4 w-4" />
      </span>
      {loading ? t("common.refreshing") : t("common.refresh")}
    </Button>
  );
}
