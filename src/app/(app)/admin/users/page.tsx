import { Suspense } from "react";

import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";
import { AdminUsersContent } from "./_components/users-content";
import { AdminUsersCardSkeleton } from "./_components/users-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminUsersPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";

  const current =
    me?.email != null && me.email.length > 0
      ? t(locale, "admin.currentUser", { email: me.email })
      : t(locale, "admin.currentUser", { email: t(locale, "common.unknown") });

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "app.admin.users")} description={t(locale, "admin.users.subtitle", { current })} />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.users.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminUsersCardSkeleton />}>
          <AdminUsersContent
            currentUserId={me?.id ?? null}
            currentUserRole={me?.role ?? null}
          />
        </Suspense>
      )}
    </div>
  );
}
