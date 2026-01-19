import { CreditCard } from "lucide-react";

import { StatsCard } from "@/components/app/stats-card";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { getCurrentUser } from "@/lib/current-user";
import { formatUsdFixed2 } from "@/lib/format";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import type { BillingLedgerListResponse } from "@/lib/types";
import { BillingTableClient } from "./billing-table-client";

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

const PAGE_SIZE = 50;

async function getLedger() {
  const res = await fetch(buildBackendUrl(`/billing/ledger?limit=${PAGE_SIZE}&offset=0`), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.billingLedger] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isBillingLedgerListResponse(json)) return null;
  return json.items;
}

export async function BillingContent() {
  const [locale, me, items] = await Promise.all([getRequestLocale(), getCurrentUser(), getLedger()]);
  const balanceUsd = me?.balance ?? 0;

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

      <BillingTableClient initialItems={items ?? []} locale={locale} />
    </div>
  );
}
