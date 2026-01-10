import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { LogsContent } from "./_components/logs-content";
import { LogsPageSkeleton } from "./_components/logs-skeleton";
import { LogsRefreshButton } from "@/components/logs/logs-refresh-button";

export const dynamic = "force-dynamic";

export default async function LogsPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "logs.title")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(locale, "logs.subtitle")}
          </p>
        </div>
        <LogsRefreshButton pageSize={50} className="rounded-xl bg-transparent" />
      </div>

      <Suspense fallback={<LogsPageSkeleton />}>
        <LogsContent locale={locale} />
      </Suspense>
    </div>
  );
}
