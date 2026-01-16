import { CreditCard } from "lucide-react";

import { StatsCard } from "@/components/app/stats-card";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { getCurrentUser } from "@/lib/current-user";
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
    next: { tags: ["billing:ledger"] },
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
          value={new Intl.NumberFormat(locale, {
            style: "currency",
            currency: "USD",
            minimumFractionDigits: 2,
            maximumFractionDigits: 2
          }).format(balanceUsd)}
          icon={CreditCard}
          className="lg:col-span-2"
        />
      </div>

      <BillingTableClient initialItems={items ?? []} locale={locale} />
    </div>
  );
}

