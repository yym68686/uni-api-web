import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { Locale } from "@/lib/i18n/messages";
import type { BillingLedgerListResponse } from "@/lib/types";
import { BillingContentClient } from "./billing-content-client";

export const BILLING_PAGE_SIZE = 50;

interface BillingContentProps {
  locale: Locale;
  initialItems: BillingLedgerListResponse["items"];
  pageSize?: number;
}

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

export async function getLedger(pageSize = BILLING_PAGE_SIZE) {
  const res = await fetch(buildBackendUrl(`/billing/ledger?limit=${pageSize}&offset=0`), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.billingLedger], revalidate: 30 },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isBillingLedgerListResponse(json)) return null;
  return json.items;
}

export function BillingContent({ locale, initialItems, pageSize = BILLING_PAGE_SIZE }: BillingContentProps) {
  return <BillingContentClient locale={locale} initialItems={initialItems} pageSize={pageSize} />;
}
