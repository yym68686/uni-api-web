import { Suspense } from "react";
import { Shield } from "lucide-react";

import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";
import { AdminUsersContent } from "./_components/users-content";
import { AdminUsersCardSkeleton } from "./_components/users-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminUsersPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const isAdmin = me?.role === "admin" || me?.role === "owner";

  const current =
    me?.email != null && me.email.length > 0
      ? t(locale, "admin.currentUser", { email: me.email })
      : t(locale, "admin.currentUser", { email: t(locale, "common.unknown") });

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "app.admin.users")} description={t(locale, "admin.users.subtitle", { current })} />

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.users.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Suspense fallback={<AdminUsersCardSkeleton />}>
          <AdminUsersContent
            locale={locale}
            currentUserId={me?.id ?? null}
            currentUserRole={me?.role ?? null}
          />
        </Suspense>
      )}
    </div>
  );
}
