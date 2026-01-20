"use client";

import * as React from "react";
import { Boxes } from "lucide-react";

import { ModelRowActions } from "@/components/admin/model-row-actions";
import { CopyableModelId } from "@/components/models/copyable-model-id";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AdminModelItem, AdminModelsListResponse } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { UI_EVENTS } from "@/lib/ui-events";

function isAdminModelsListResponse(value: unknown): value is AdminModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatUsdPerM(value: string | null | undefined) {
  if (!value) return "—";
  return `$${value}`;
}

interface AdminModelsTableClientProps {
  initialItems: AdminModelItem[];
}

export function AdminModelsTableClient({ initialItems }: AdminModelsTableClientProps) {
  const { t } = useI18n();
  const [items, setItems] = React.useState<AdminModelItem[]>(initialItems);

  function upsert(next: AdminModelItem) {
    setItems((prev) => {
      const idx = prev.findIndex((m) => m.model === next.model);
      if (idx === -1) return [next, ...prev];
      const copy = [...prev];
      copy[idx] = next;
      return copy;
    });
  }

  async function refetch() {
    const res = await fetch("/api/admin/models", { cache: "no-store" });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) return;
    if (!isAdminModelsListResponse(json)) return;
    setItems(json.items);
  }

  React.useEffect(() => {
    function onRefreshed() {
      void refetch();
    }
    window.addEventListener(UI_EVENTS.adminModelsRefreshed, onRefreshed);
    return () => window.removeEventListener(UI_EVENTS.adminModelsRefreshed, onRefreshed);
  }, []);

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState
            icon={(
              <span className="inline-flex uai-float-sm">
                <Boxes className="h-6 w-6 text-muted-foreground" />
              </span>
            )}
            title={t("admin.models.empty")}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table variant="card">
            <TableHeader>
              <TableRow>
                <TableHead>{t("models.table.model")}</TableHead>
                <TableHead>{t("keys.table.status")}</TableHead>
                <TableHead>{t("models.table.input")}</TableHead>
                <TableHead>{t("models.table.output")}</TableHead>
                <TableHead>{t("admin.models.table.sources")}</TableHead>
                <TableHead className="w-12 text-right">{t("keys.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((m) => (
                <TableRow key={m.model} className="uai-cv-auto">
                  <TableCell>
                    <CopyableModelId value={m.model} />
                  </TableCell>
                  <TableCell>
                    {!m.available ? (
                      <Badge variant="outline">{t("admin.models.badge.missing")}</Badge>
                    ) : m.enabled ? (
                      <Badge variant="success">{t("admin.models.badge.enabled")}</Badge>
                    ) : (
                      <Badge variant="destructive">{t("admin.models.badge.disabled")}</Badge>
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {formatUsdPerM(m.inputUsdPerM)}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {formatUsdPerM(m.outputUsdPerM)}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{m.sources}</TableCell>
                  <TableCell className="text-right">
                    <ModelRowActions model={m} onUpdated={upsert} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
    </Card>
  );
}
