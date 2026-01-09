import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { getCurrentUser } from "@/lib/current-user";
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
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const userName = me?.email && me.email.length > 0 ? me.email : "User";
  const remainingCredits = typeof me?.balance === "number" && Number.isFinite(me.balance) ? me.balance : null;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          {t(locale, "dashboard.welcomeBack", { name: userName })}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "dashboard.subtitle")}
        </p>
      </div>

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
