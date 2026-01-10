import { Suspense } from "react";
import { Megaphone } from "lucide-react";

import { AnnouncementPublisher } from "@/components/admin/announcement-publisher";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Megaphone className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.ann.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Suspense fallback={<AdminAnnouncementsCardSkeleton showActions />}>
          <AdminAnnouncementsContent locale={locale} isAdmin />
        </Suspense>
      )}
    </div>
  );
}
