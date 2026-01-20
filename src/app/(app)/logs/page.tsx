import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { getLogs, LOGS_PAGE_SIZE, LogsContent } from "./_components/logs-content";
import { LogsContentSkeleton } from "./_components/logs-skeleton";
import { LogsRefreshButton } from "@/components/logs/logs-refresh-button";
import { PageHeader } from "@/components/common/page-header";

export const dynamic = "force-dynamic";

export default async function LogsPage() {
  const localePromise = getRequestLocale();
  const logsPromise = getLogs();
  const locale = await localePromise;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "logs.title")}
        description={t(locale, "logs.subtitle")}
        actions={<LogsRefreshButton pageSize={LOGS_PAGE_SIZE} className="rounded-xl bg-transparent" />}
      />

      <Suspense fallback={<LogsContentSkeleton />}>
        <LogsContent initialItemsPromise={logsPromise} />
      </Suspense>
    </div>
  );
}
