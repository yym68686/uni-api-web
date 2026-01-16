import { Suspense } from "react";

import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { BillingContent } from "./_components/billing-content";
import { BillingPageSkeleton } from "./_components/billing-skeleton";

export const dynamic = "force-dynamic";

export default async function BillingPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "billing.title")} description={t(locale, "billing.subtitle")} />
      <Suspense fallback={<BillingPageSkeleton />}>
        <BillingContent />
      </Suspense>
    </div>
  );
}

