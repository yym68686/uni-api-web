import { Suspense } from "react";
import { Megaphone } from "lucide-react";

import { AnnouncementPublisher } from "@/components/admin/announcement-publisher";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.announcements")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(locale, "admin.ann.subtitle", { current })}
          </p>
        </div>
        {isAdmin ? <AnnouncementPublisher /> : null}
      </div>

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
      ) : null}

      <Suspense fallback={<AdminAnnouncementsCardSkeleton showActions={isAdmin} />}>
        <AdminAnnouncementsContent locale={locale} isAdmin={isAdmin} />
      </Suspense>
    </div>
  );
}
