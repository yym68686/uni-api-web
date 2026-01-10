"use client";

import * as React from "react";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";

const MODELS_REFRESHED_EVENT = "uai:admin:models:refreshed";

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function AdminModelsRefreshButton() {
  const [loading, setLoading] = React.useState(false);
  const { t } = useI18n();

  async function refresh() {
    if (loading) return;
    setLoading(true);
    try {
      const res = await fetch("/api/admin/models/refresh", { method: "POST" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("admin.models.refreshFailed")));
      toast.success(t("admin.models.refreshSuccess"));
      window.dispatchEvent(new Event(MODELS_REFRESHED_EVENT));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("admin.models.refreshFailed"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <Button
      type="button"
      variant="outline"
      className="rounded-xl bg-transparent"
      disabled={loading}
      onClick={() => void refresh()}
    >
      <RefreshCw className="h-4 w-4" />
      {loading ? t("common.refreshing") : t("common.refresh")}
    </Button>
  );
}
