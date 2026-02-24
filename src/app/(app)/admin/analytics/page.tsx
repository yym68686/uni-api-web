import { Suspense } from "react";

import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";
import { AdminAnalyticsContent } from "./_components/analytics-content";
import { AdminAnalyticsSkeleton } from "./_components/analytics-skeleton";

export const dynamic = "force-dynamic";

interface AdminAnalyticsPageProps {
  searchParams?: Record<string, string | string[] | undefined> | Promise<Record<string, string | string[] | undefined>>;
}

export default async function AdminAnalyticsPage({ searchParams }: AdminAnalyticsPageProps) {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const resolvedSearchParams = ((await searchParams) ?? {}) satisfies Record<string, string | string[] | undefined>;

  const current =
    me?.email != null && me.email.length > 0
      ? t(locale, "admin.currentUser", { email: me.email })
      : t(locale, "admin.currentUser", { email: t(locale, "common.unknown") });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.analytics")}
        description={t(locale, "admin.analytics.subtitle", { current })}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.analytics.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminAnalyticsSkeleton />}>
          <AdminAnalyticsContent searchParams={resolvedSearchParams} />
        </Suspense>
      )}
    </div>
  );
}
