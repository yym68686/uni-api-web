import { Suspense } from "react";

import { AdminModelsRefreshButton } from "@/components/admin/models-refresh-button";
import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminModelPricingContent } from "./_components/model-pricing-content";
import { AdminModelsContent } from "./_components/models-content";
import { AdminModelPricingCardSkeleton, AdminModelsCardSkeleton } from "./_components/models-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminModelsPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.modelConfig")}
        description={t(locale, "admin.models.subtitle", { current })}
        actions={isAdmin ? <AdminModelsRefreshButton /> : null}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.models.forbidden")}
        />
      ) : (
        <>
          <Suspense fallback={<AdminModelsCardSkeleton />}>
            <AdminModelsContent />
          </Suspense>
          <Suspense fallback={<AdminModelPricingCardSkeleton />}>
            <AdminModelPricingContent />
          </Suspense>
        </>
      )}
    </div>
  );
}
