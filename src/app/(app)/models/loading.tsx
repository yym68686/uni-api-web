"use client";

import { PageHeader } from "@/components/common/page-header";
import { useI18n } from "@/components/i18n/i18n-provider";
import { peekSwrLite } from "@/lib/swr-lite";
import { API_PATHS } from "@/lib/api-paths";
import type { ModelsListResponse } from "@/lib/types";

import { ModelsContentClient } from "./_components/models-content-client";
import { ModelsPageSkeleton } from "./_components/models-skeleton";

export default function Loading() {
  const { locale, t } = useI18n();
  const cached = peekSwrLite<ModelsListResponse["items"]>(API_PATHS.models);
  if (cached === undefined) return <ModelsPageSkeleton />;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("models.title")}
        description={
          <>
            <div>{t("models.subtitle")}</div>
            <div className="mt-1 text-xs text-muted-foreground">{t("models.card.desc")}</div>
          </>
        }
      />

      <ModelsContentClient locale={locale} initialItems={cached} autoRevalidate={false} />
    </div>
  );
}

