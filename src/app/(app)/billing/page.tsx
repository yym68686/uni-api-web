import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { BillingContent, getBillingSettings, getLedger } from "./_components/billing-content";

export const dynamic = "force-dynamic";

export default async function BillingPage() {
  const [locale, items, settings, me] = await Promise.all([getRequestLocale(), getLedger(), getBillingSettings(), getCurrentUser()]);
  const topupEnabled = settings?.billingTopupEnabled ?? true;
  const initialBalance = typeof me?.balance === "number" && Number.isFinite(me.balance) ? me.balance : null;

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "billing.title")} description={t(locale, "billing.subtitle")} />
      <BillingContent locale={locale} initialItems={items ?? []} initialBalance={initialBalance} topupEnabled={topupEnabled} />
    </div>
  );
}
