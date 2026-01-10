import { Suspense } from "react";

import { AnnouncementPublisher } from "@/components/admin/announcement-publisher";
import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminAnnouncementsContent } from "./_components/announcements-content";
import { AdminAnnouncementsCardSkeleton } from "./_components/announcements-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminAnnouncementsPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();

  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.announcements")}
        description={t(locale, "admin.ann.subtitle", { current })}
        actions={isAdmin ? <AnnouncementPublisher /> : null}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.ann.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminAnnouncementsCardSkeleton showActions />}>
          <AdminAnnouncementsContent locale={locale} isAdmin />
        </Suspense>
      )}
    </div>
  );
}
