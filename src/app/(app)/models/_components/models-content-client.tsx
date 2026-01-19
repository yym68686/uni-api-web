"use client";

import { Boxes } from "lucide-react";
import * as React from "react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { API_PATHS } from "@/lib/api-paths";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { ModelsListResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";
import { ModelsContentSkeleton } from "@/app/(app)/models/_components/models-skeleton";

function isModelsListResponse(value: unknown): value is ModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function fetchModels() {
  const res = await fetch(API_PATHS.models, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isModelsListResponse(json)) throw new Error("Invalid response");
  return json.items;
}

function formatUsdPerM(value: string | null | undefined) {
  if (!value) return "—";
  return `$${value}`;
}

interface ModelsContentClientProps {
  locale: Locale;
  initialItems: ModelsListResponse["items"] | null;
  autoRevalidate?: boolean;
}

export function ModelsContentClient({ locale, initialItems, autoRevalidate = true }: ModelsContentClientProps) {
  const { data, mutate } = useSwrLite<ModelsListResponse["items"]>(API_PATHS.models, fetchModels, {
    fallbackData: initialItems ?? undefined,
    dedupingIntervalMs: 0,
    revalidateOnFocus: false
  });

  React.useEffect(() => {
    if (!autoRevalidate) return;
    void mutate(undefined, { revalidate: true });
  }, [autoRevalidate, mutate]);

  if (data === undefined && initialItems === null) return <ModelsContentSkeleton />;

  const items = data ?? initialItems ?? [];

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState
            icon={<Boxes className="h-6 w-6 text-muted-foreground uai-float-sm" />}
            title={t(locale, "models.empty")}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table variant="card">
            <TableHeader>
              <TableRow>
                <TableHead>{t(locale, "models.table.model")}</TableHead>
                <TableHead>{t(locale, "models.table.input")}</TableHead>
                <TableHead>{t(locale, "models.table.output")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((m) => (
                <TableRow key={m.model}>
                  <TableCell>
                    <CopyableModelId value={m.model} />
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {formatUsdPerM(m.inputUsdPerM)}
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {formatUsdPerM(m.outputUsdPerM)}
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
