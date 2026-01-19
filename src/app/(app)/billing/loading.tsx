"use client";

import { PageHeader } from "@/components/common/page-header";
import { useI18n } from "@/components/i18n/i18n-provider";
import { billingLedgerListApiPath } from "@/lib/api-paths";
import { peekSwrLite } from "@/lib/swr-lite";
import type { BillingLedgerItem } from "@/lib/types";

import { BillingContentClient } from "./_components/billing-content-client";
import { BillingPageSkeleton } from "./_components/billing-skeleton";

const PAGE_SIZE = 50;

export default function Loading() {
  const { locale, t } = useI18n();
  const key = billingLedgerListApiPath(PAGE_SIZE, 0);
  const cached = peekSwrLite<BillingLedgerItem[]>(key);
  if (cached === undefined) {
    return (
      <div className="space-y-6">
        <PageHeader title={t("billing.title")} description={t("billing.subtitle")} />
        <BillingPageSkeleton />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader title={t("billing.title")} description={t("billing.subtitle")} />
      <BillingContentClient locale={locale} initialItems={cached} pageSize={PAGE_SIZE} autoRevalidate={false} />
    </div>
  );
}

