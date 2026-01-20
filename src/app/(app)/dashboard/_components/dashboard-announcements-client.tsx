"use client";

import { Megaphone } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { AnnouncementsListResponse } from "@/lib/types";
import { getAnnouncementMeta, getAnnouncementTitle } from "@/lib/announcements";

interface DashboardAnnouncementsClientProps {
  initialItems: AnnouncementsListResponse["items"];
}

export function DashboardAnnouncementsClient({
  initialItems
}: DashboardAnnouncementsClientProps) {
  const { locale, t } = useI18n();
  const announcements = initialItems;

  return (
    <Card className="bg-warning/10">
      <CardHeader>
        <CardTitle>{t("dashboard.ann.title")}</CardTitle>
        <CardDescription>{t("dashboard.ann.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        {announcements.length === 0 ? (
          <EmptyState
            className="bg-background/20 p-6"
            icon={(
              <span className="inline-flex uai-float-sm">
              <Megaphone className="h-6 w-6 text-muted-foreground" />
            </span>
            )}
            title={t("dashboard.ann.empty")}
          />
        ) : (
          <div className="space-y-3">
            {announcements.map((a) => (
              <div
                key={a.id}
                className={cn(
                  "rounded-xl border border-border bg-background/35 p-3",
                  "transition-all duration-300 hover:shadow-lg hover:scale-[1.01]"
                )}
              >
                <div className="text-sm font-medium text-foreground">{getAnnouncementTitle(a, locale)}</div>
                <div className="mt-1 font-mono tabular-nums text-xs text-muted-foreground">
                  <ClientDateTime value={a.createdAt} locale={locale} /> · {getAnnouncementMeta(a, locale)}
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
