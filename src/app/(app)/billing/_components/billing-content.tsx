import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { BillingLedgerListResponse, BillingPaymentMethod } from "@/lib/types";
import { BillingContentClient } from "./billing-content-client";

export const BILLING_PAGE_SIZE = 50;

interface BillingSettingsResponse {
  billingTopupEnabled: boolean;
  billingPaymentCardEnabled: boolean;
  billingPaymentAlipayEnabled: boolean;
  billingPaymentWxpayEnabled: boolean;
}

interface BillingContentProps {
  locale: Locale;
  initialItems: BillingLedgerListResponse["items"];
  initialBalance: number | null;
  topupEnabled: boolean;
  availablePaymentMethods: BillingPaymentMethod[];
  pageSize?: number;
}

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function normalizeBillingSettingsResponse(value: unknown): BillingSettingsResponse | null {
  if (!value || typeof value !== "object") return null;
  const obj = value as Record<string, unknown>;
  const billingTopupEnabled = obj.billingTopupEnabled ?? obj.billing_topup_enabled;
  if (typeof billingTopupEnabled !== "boolean") return null;
  const billingPaymentCardEnabled = obj.billingPaymentCardEnabled ?? obj.billing_payment_card_enabled;
  const billingPaymentAlipayEnabled = obj.billingPaymentAlipayEnabled ?? obj.billing_payment_alipay_enabled;
  const billingPaymentWxpayEnabled = obj.billingPaymentWxpayEnabled ?? obj.billing_payment_wxpay_enabled;

  return {
    billingTopupEnabled,
    billingPaymentCardEnabled: typeof billingPaymentCardEnabled === "boolean" ? billingPaymentCardEnabled : true,
    billingPaymentAlipayEnabled: typeof billingPaymentAlipayEnabled === "boolean" ? billingPaymentAlipayEnabled : true,
    billingPaymentWxpayEnabled: typeof billingPaymentWxpayEnabled === "boolean" ? billingPaymentWxpayEnabled : true
  };
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
  return normalizeBillingSettingsResponse(json);
}

export function BillingContent({
  locale,
  initialItems,
  initialBalance,
  topupEnabled,
  availablePaymentMethods,
  pageSize = BILLING_PAGE_SIZE
}: BillingContentProps) {
  return (
    <BillingContentClient
      locale={locale}
      initialItems={initialItems}
      initialBalance={initialBalance}
      pageSize={pageSize}
      topupEnabled={topupEnabled}
      availablePaymentMethods={availablePaymentMethods}
    />
  );
}
