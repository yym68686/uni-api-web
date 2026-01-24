"use client";

import { Boxes } from "lucide-react";
import * as React from "react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
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

function formatDiscountPercent(discount: number) {
  const raw = (1 - discount) * 100;
  const pct = Math.round(raw);
  return pct <= 0 ? null : pct;
}

interface PriceCellProps {
  price: string | null | undefined;
  original: string | null | undefined;
  discount: number | null | undefined;
}

function PriceCell({ price, original, discount }: PriceCellProps) {
  if (!price) {
    return <span className="font-mono tabular-nums text-xs text-muted-foreground">—</span>;
  }

  const hasDiscount = typeof discount === "number" && discount > 0 && discount < 1 && Boolean(original);
  const pct = hasDiscount ? formatDiscountPercent(discount) : null;

  return (
    <div className="flex items-center gap-2">
      <span className="font-mono tabular-nums text-xs text-foreground">{formatUsdPerM(price)}</span>
      {hasDiscount ? (
        <>
          <span className="font-mono tabular-nums text-xs text-muted-foreground line-through">
            {formatUsdPerM(original)}
          </span>
          {pct != null ? (
            <Badge variant="success" className="rounded-full px-2 py-0 text-[10px]">
              -{pct}%
            </Badge>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

interface ModelsContentClientProps {
  locale: Locale;
  initialItems: ModelsListResponse["items"] | null;
  autoRevalidate?: boolean;
}

export function ModelsContentClient({ locale, initialItems, autoRevalidate = true }: ModelsContentClientProps) {
  const [hydrated, setHydrated] = React.useState(false);
  const { data, mutate } = useSwrLite<ModelsListResponse["items"]>(API_PATHS.models, fetchModels, {
    fallbackData: initialItems ?? undefined,
    revalidateOnFocus: false
  });

  React.useEffect(() => {
    setHydrated(true);
  }, []);

  React.useEffect(() => {
    if (!autoRevalidate) return;
    void mutate(undefined, { revalidate: true });
  }, [autoRevalidate, mutate]);

  if (data === undefined && initialItems === null) return <ModelsContentSkeleton />;

  const items = hydrated ? (data ?? initialItems ?? []) : (initialItems ?? []);

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
                <TableRow key={m.model} className="uai-cv-auto">
                  <TableCell>
                    <CopyableModelId value={m.model} />
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    <PriceCell
                      price={m.inputUsdPerM}
                      original={m.inputUsdPerMOriginal}
                      discount={m.discount}
                    />
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    <PriceCell
                      price={m.outputUsdPerM}
                      original={m.outputUsdPerMOriginal}
                      discount={m.discount}
                    />
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
