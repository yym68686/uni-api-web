import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { getCurrentUser } from "@/lib/current-user";
import { PageHeader } from "@/components/common/page-header";
import { DashboardKpis } from "./_components/dashboard-kpis";
import { DashboardChart } from "./_components/dashboard-chart";
import { DashboardAnnouncements } from "./_components/dashboard-announcements";
import {
  DashboardAnnouncementsSkeleton,
  DashboardChartSkeleton,
  DashboardKpisSkeleton
} from "./_components/dashboard-skeleton";

export const dynamic = "force-dynamic";

export default async function DashboardPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const userName = me?.email && me.email.length > 0 ? me.email : "User";
  const remainingCredits = typeof me?.balance === "number" && Number.isFinite(me.balance) ? me.balance : null;

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "dashboard.welcomeBack", { name: userName })} description={t(locale, "dashboard.subtitle")} />

      <Suspense fallback={<DashboardKpisSkeleton />}>
        <DashboardKpis locale={locale} remainingCredits={remainingCredits} />
      </Suspense>

      <div className="grid gap-4 lg:grid-cols-3">
        <Suspense fallback={<DashboardChartSkeleton />}>
          <DashboardChart locale={locale} />
        </Suspense>

        <Suspense fallback={<DashboardAnnouncementsSkeleton />}>
          <DashboardAnnouncements locale={locale} />
        </Suspense>
      </div>
    </div>
  );
}
