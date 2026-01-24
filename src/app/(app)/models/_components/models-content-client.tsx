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

interface PricePartProps {
  price: string | null | undefined;
}

function formatDiscountZhe(discount: number) {
  const zhe = Math.round(discount * 100) / 10;
  if (!Number.isFinite(zhe)) return null;
  const normalized = zhe.toFixed(1).replace(/\.0$/, "");
  return normalized === "0" ? null : normalized;
}

function PricePart({ price }: PricePartProps) {
  return <span className="font-mono tabular-nums text-xs text-foreground">{formatUsdPerM(price)}</span>;
}

interface PriceSummaryProps {
  locale: Locale;
  input: string | null | undefined;
  output: string | null | undefined;
  discount: number | null | undefined;
}

function PriceSummary({ locale, input, output, discount }: PriceSummaryProps) {
  const hasDiscount = typeof discount === "number" && discount > 0 && discount < 1;
  const pct = hasDiscount ? formatDiscountPercent(discount) : null;
  const zhe = hasDiscount ? formatDiscountZhe(discount) : null;
  const badge =
    locale === "zh-CN"
      ? zhe
        ? t(locale, "models.discountBadge", { zhe })
        : null
      : pct != null
        ? t(locale, "models.discountBadge", { pct })
        : null;

  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <PricePart price={input} />
      <span className="text-xs text-muted-foreground">/</span>
      <PricePart price={output} />
      {badge ? (
        <Badge variant="success" className="ml-1 rounded-full px-2 py-0 text-[10px]">
          {badge}
        </Badge>
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
                <TableHead>{t(locale, "models.table.price")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((m) => (
                <TableRow key={m.model} className="uai-cv-auto">
                  <TableCell>
                    <CopyableModelId value={m.model} />
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    <PriceSummary
                      locale={locale}
                      input={m.inputUsdPerM}
                      output={m.outputUsdPerM}
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
