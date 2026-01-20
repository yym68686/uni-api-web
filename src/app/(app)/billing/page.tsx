import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { BillingContent, getLedger } from "./_components/billing-content";

export const dynamic = "force-dynamic";

export default async function BillingPage() {
  const [locale, items] = await Promise.all([getRequestLocale(), getLedger()]);

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "billing.title")} description={t(locale, "billing.subtitle")} />
      <BillingContent locale={locale} initialItems={items ?? []} />
    </div>
  );
}
