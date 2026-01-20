import { Suspense } from "react";

import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";
import { AdminOverviewContent } from "./_components/overview-content";
import { AdminOverviewSkeleton } from "./_components/overview-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminOverviewPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";

  const current =
    me?.email != null && me.email.length > 0
      ? t(locale, "admin.currentUser", { email: me.email })
      : t(locale, "admin.currentUser", { email: t(locale, "common.unknown") });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.overview")}
        description={t(locale, "admin.overview.subtitle", { current })}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.overview.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminOverviewSkeleton />}>
          <AdminOverviewContent locale={locale} />
        </Suspense>
      )}
    </div>
  );
}
