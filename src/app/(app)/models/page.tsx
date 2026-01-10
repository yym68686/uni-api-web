import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { PageHeader } from "@/components/common/page-header";
import { ModelsContent } from "./_components/models-content";
import { ModelsContentSkeleton } from "./_components/models-skeleton";

export const dynamic = "force-dynamic";

export default async function ModelsPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <PageHeader
        title={t(locale, "models.title")}
        description={
          <>
            <div>{t(locale, "models.subtitle")}</div>
            <div className="mt-1 text-xs text-muted-foreground">{t(locale, "models.card.desc")}</div>
          </>
        }
      />

      <Suspense fallback={<ModelsContentSkeleton />}>
        <ModelsContent locale={locale} />
      </Suspense>
    </div>
  );
}
