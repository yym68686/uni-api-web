import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { Locale } from "@/lib/i18n/messages";
import type { BillingLedgerListResponse } from "@/lib/types";
import { BillingContentClient } from "./billing-content-client";

const PAGE_SIZE = 50;

interface BillingContentProps {
  locale: Locale;
}

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getLedger() {
  const res = await fetch(buildBackendUrl(`/billing/ledger?limit=${PAGE_SIZE}&offset=0`), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.billingLedger], revalidate: 30 },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isBillingLedgerListResponse(json)) return null;
  return json.items;
}

export async function BillingContent({ locale }: BillingContentProps) {
  const items = (await getLedger()) ?? [];
  return <BillingContentClient locale={locale} initialItems={items} pageSize={PAGE_SIZE} />;
}
