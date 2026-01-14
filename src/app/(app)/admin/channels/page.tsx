import { Suspense } from "react";

import { ChannelPublisher } from "@/components/admin/channel-publisher";
import { AdminForbiddenCard } from "@/components/admin/admin-forbidden-card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminChannelsContent } from "./_components/channels-content";
import { AdminChannelsCardSkeleton } from "./_components/channels-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminChannelsPage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "app.admin.channels")}
        description={t(locale, "admin.channels.subtitle", { current })}
        actions={isAdmin ? <ChannelPublisher /> : null}
      />

      {!isAdmin ? (
        <AdminForbiddenCard
          title={t(locale, "admin.forbidden")}
          description={t(locale, "admin.channels.forbidden")}
        />
      ) : (
        <Suspense fallback={<AdminChannelsCardSkeleton />}>
          <AdminChannelsContent />
        </Suspense>
      )}
    </div>
  );
}
