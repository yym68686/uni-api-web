import { Suspense } from "react";

import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { ModelsContent } from "./_components/models-content";
import { ModelsPageSkeleton } from "./_components/models-skeleton";

export const dynamic = "force-dynamic";

export default async function ModelsPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "models.title")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "models.subtitle")}
        </p>
      </div>

      <Suspense fallback={<ModelsPageSkeleton />}>
        <ModelsContent locale={locale} />
      </Suspense>
    </div>
  );
}
