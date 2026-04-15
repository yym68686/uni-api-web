"use client";

import * as React from "react";
import { Download, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import { logsExportApiPath } from "@/lib/api-paths";
import { cn } from "@/lib/utils";

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if (!("message" in json)) return fallback;
  const message = (json as { message?: unknown }).message;
  return typeof message === "string" && message.trim().length > 0 ? message : fallback;
}

function readDownloadFilename(disposition: string | null) {
  if (!disposition) return null;

  const utf8Match = disposition.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1]);
    } catch {
      return utf8Match[1];
    }
  }

  const quotedMatch = disposition.match(/filename="([^"]+)"/i);
  if (quotedMatch?.[1]) return quotedMatch[1];

  const plainMatch = disposition.match(/filename=([^;]+)/i);
  return plainMatch?.[1]?.trim() ?? null;
}

interface LogsExportButtonProps {
  className?: string;
}

export function LogsExportButton({ className }: LogsExportButtonProps) {
  const { t } = useI18n();
  const [loading, setLoading] = React.useState(false);

  async function exportLogs() {
    if (loading) return;
    setLoading(true);

    try {
      const res = await fetch(logsExportApiPath(), { cache: "no-store" });

      if (!res.ok) {
        const contentType = res.headers.get("content-type") ?? "";
        const json: unknown = contentType.includes("application/json")
          ? await res.json().catch(() => null)
          : null;
        throw new Error(readMessage(json, t("common.operationFailed")));
      }

      const blob = await res.blob();
      const objectUrl = URL.createObjectURL(blob);
      const filename = readDownloadFilename(res.headers.get("content-disposition")) ?? "logs.csv";
      const anchor = document.createElement("a");

      anchor.href = objectUrl;
      anchor.download = filename;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1_000);

      toast.success(t("logs.export.success"));
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
      onClick={() => void exportLogs()}
    >
      <span className={cn("inline-flex", loading ? "animate-spin" : null)}>
        {loading ? <Loader2 className="h-4 w-4" /> : <Download className="h-4 w-4" />}
      </span>
      {loading ? t("logs.export.exporting") : t("logs.export.button")}
    </Button>
  );
}
