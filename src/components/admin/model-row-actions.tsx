"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { MoreVertical, Power, PowerOff } from "lucide-react";
import { toast } from "sonner";

import type { AdminModelItem, AdminModelUpdateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";

interface ModelRowActionsProps {
  model: AdminModelItem;
  onUpdated?: (next: AdminModelItem) => void;
  className?: string;
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function ModelRowActions({ model, onUpdated, className }: ModelRowActionsProps) {
  const router = useRouter();
  const [submitting, setSubmitting] = React.useState(false);
  const { t } = useI18n();

  async function patch(payload: Record<string, unknown>) {
    const res = await fetch(`/api/admin/models/${encodeURIComponent(model.model)}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload)
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error(readMessage(json, t("common.updateFailed")));
    const next = json as AdminModelUpdateResponse;
    return next.item;
  }

  async function toggleEnabled(nextEnabled: boolean) {
    setSubmitting(true);
    try {
      onUpdated?.({ ...model, enabled: nextEnabled });
      const next = await patch({ enabled: nextEnabled });
      onUpdated?.(next);
      toast.success(nextEnabled ? t("admin.models.toast.enabled") : t("admin.models.toast.disabled"));
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(model);
      toast.error(err instanceof Error ? err.message : t("common.updateFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className={cn("h-8 w-8 rounded-xl", className)}
          aria-label={t("common.actions")}
        >
          <MoreVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem
          disabled={submitting}
          onSelect={(e) => {
            e.preventDefault();
            void toggleEnabled(!model.enabled);
          }}
        >
          {model.enabled ? (
            <>
              <PowerOff className="mr-2 h-4 w-4" />
              {t("admin.models.actions.disable")}
            </>
          ) : (
            <>
              <Power className="mr-2 h-4 w-4" />
              {t("admin.models.actions.enable")}
            </>
          )}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
