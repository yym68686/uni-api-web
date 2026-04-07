import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { BillingLedgerListResponse } from "@/lib/types";
import { BillingContentClient } from "./billing-content-client";

export const BILLING_PAGE_SIZE = 50;

interface BillingSettingsResponse {
  billingTopupEnabled: boolean;
}

interface BillingContentProps {
  locale: Locale;
  initialItems: BillingLedgerListResponse["items"];
  initialBalance: number | null;
  topupEnabled: boolean;
  pageSize?: number;
}

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isBillingSettingsResponse(value: unknown): value is BillingSettingsResponse {
  if (!value || typeof value !== "object") return false;
  return typeof (value as { billingTopupEnabled?: unknown }).billingTopupEnabled === "boolean";
}

export async function getLedger(pageSize = BILLING_PAGE_SIZE) {
  const res = await fetch(buildBackendUrl(`/billing/ledger?limit=${pageSize}&offset=0`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isBillingLedgerListResponse(json)) return null;
  return json.items;
}

export async function getBillingSettings() {
  const res = await fetch(buildBackendUrl("/billing/settings"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  if (!isBillingSettingsResponse(json)) return null;
  return json;
}

export function BillingContent({ locale, initialItems, initialBalance, topupEnabled, pageSize = BILLING_PAGE_SIZE }: BillingContentProps) {
  return (
    <BillingContentClient
      locale={locale}
      initialItems={initialItems}
      initialBalance={initialBalance}
      pageSize={pageSize}
      topupEnabled={topupEnabled}
    />
  );
}
