import { Suspense } from "react";
import { Boxes } from "lucide-react";

import { AdminModelsRefreshButton } from "@/components/admin/models-refresh-button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminModelsContent } from "./_components/models-content";
import { AdminModelsCardSkeleton } from "./_components/models-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminModelsPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.modelConfig")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(locale, "admin.models.subtitle", { current })}
          </p>
        </div>
        {isAdmin ? <AdminModelsRefreshButton /> : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Boxes className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.models.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Suspense fallback={<AdminModelsCardSkeleton />}>
          <AdminModelsContent locale={locale} />
        </Suspense>
      )}
    </div>
  );
}
