import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { LogsContent } from "./_components/logs-content";
import { LogsPageSkeleton } from "./_components/logs-skeleton";
import { LogsRefreshButton } from "@/components/logs/logs-refresh-button";
import { PageHeader } from "@/components/common/page-header";

export const dynamic = "force-dynamic";

export default async function LogsPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "logs.title")}
        description={t(locale, "logs.subtitle")}
        actions={<LogsRefreshButton pageSize={50} className="rounded-xl bg-transparent" />}
      />

      <Suspense fallback={<LogsPageSkeleton />}>
        <LogsContent locale={locale} />
      </Suspense>
    </div>
  );
}
