"use client";

import { CreditCard } from "lucide-react";
import * as React from "react";

import { StatsCard } from "@/components/app/stats-card";
import { BillingTableClient } from "./billing-table-client";
import { billingLedgerListApiPath } from "@/lib/api-paths";
import { formatUsdFixed2 } from "@/lib/format";
import { useSwrLite } from "@/lib/swr-lite";
import type { BillingLedgerItem, BillingLedgerListResponse } from "@/lib/types";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { BillingPageSkeleton } from "./billing-skeleton";

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function fetchLedger(key: string) {
  const res = await fetch(key, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isBillingLedgerListResponse(json)) throw new Error("Invalid response");
  return json.items;
}

interface BillingContentClientProps {
  locale: Locale;
  initialItems: BillingLedgerItem[] | null;
  pageSize: number;
  autoRevalidate?: boolean;
}

export function BillingContentClient({ locale, initialItems, pageSize, autoRevalidate = true }: BillingContentClientProps) {
  const key = billingLedgerListApiPath(pageSize, 0);
  const { data, mutate } = useSwrLite<BillingLedgerItem[]>(key, fetchLedger, {
    fallbackData: initialItems ?? undefined,
    revalidateOnFocus: false
  });

  // Silent revalidate once after mount (no skeleton).
  React.useEffect(() => {
    if (!autoRevalidate) return;
    void mutate(undefined, { revalidate: true });
  }, [autoRevalidate, mutate]);

  if (data === undefined && initialItems === null) return <BillingPageSkeleton />;

  const items = data ?? initialItems ?? [];
  const balanceUsd = items.length > 0 ? Number(items[0]?.balanceUsd ?? 0) : 0;

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard
          title={t(locale, "billing.kpi.balance")}
          value={formatUsdFixed2(balanceUsd, locale)}
          icon={CreditCard}
          className="lg:col-span-2"
        />
      </div>

      <BillingTableClient initialItems={items} locale={locale} />
    </div>
  );
}
