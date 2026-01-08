"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function AdminModelsRefreshButton() {
  const router = useRouter();
  const [loading, setLoading] = React.useState(false);

  async function refresh() {
    if (loading) return;
    setLoading(true);
    try {
      const res = await fetch("/api/admin/models/refresh", { method: "POST" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, "刷新失败"));
      toast.success("已刷新模型缓存");
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "刷新失败");
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
      {loading ? "Refreshing…" : "Refresh"}
    </Button>
  );
}

