import { Suspense } from "react";
import { Settings } from "lucide-react";

import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminSettingsContent } from "./_components/settings-content";
import { AdminSettingsCardSkeleton } from "./_components/settings-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminSettingsPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.settings")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "admin.settings.subtitle", { current })}
        </p>
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Settings className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.settings.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Suspense fallback={<AdminSettingsCardSkeleton />}>
          <AdminSettingsContent locale={locale} />
        </Suspense>
      )}
    </div>
  );
}

