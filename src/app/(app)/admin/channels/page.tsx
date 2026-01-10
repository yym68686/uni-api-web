import { Suspense } from "react";
import { PlugZap } from "lucide-react";

import { ChannelPublisher } from "@/components/admin/channel-publisher";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PageHeader } from "@/components/common/page-header";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { AdminChannelsContent } from "./_components/channels-content";
import { AdminChannelsCardSkeleton } from "./_components/channels-skeleton";

export const dynamic = "force-dynamic";

export default async function AdminChannelsPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
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
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <PlugZap className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.channels.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Suspense fallback={<AdminChannelsCardSkeleton />}>
          <AdminChannelsContent locale={locale} />
        </Suspense>
      )}
    </div>
  );
}
