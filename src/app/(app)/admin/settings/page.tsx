import { Suspense } from "react";

import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminSettingsContent } from "./_components/settings-content";
import { AdminSettingsCardSkeleton } from "./_components/settings-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminSettingsPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.settings")}
        description={t(locale, "admin.settings.subtitle", { current })}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.settings.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminSettingsCardSkeleton />}>
          <AdminSettingsContent locale={locale} />
        </Suspense>
      )}
    </div>
  );
}
