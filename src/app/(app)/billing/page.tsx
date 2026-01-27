import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { BillingContent, getBillingSettings, getLedger } from "./_components/billing-content";

export const dynamic = "force-dynamic";

export default async function BillingPage() {
  const [locale, items, settings] = await Promise.all([getRequestLocale(), getLedger(), getBillingSettings()]);
  const topupEnabled = settings?.billingTopupEnabled ?? true;

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "billing.title")} description={t(locale, "billing.subtitle")} />
      <BillingContent locale={locale} initialItems={items ?? []} topupEnabled={topupEnabled} />
    </div>
  );
}
