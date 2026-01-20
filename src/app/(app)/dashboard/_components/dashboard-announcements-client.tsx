"use client";

import * as React from "react";
import { Megaphone } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { AnnouncementsListResponse } from "@/lib/types";
import { getAnnouncementContent, getAnnouncementTitle } from "@/lib/announcements";

interface DashboardAnnouncementsClientProps {
  initialItems: AnnouncementsListResponse["items"];
}

function preview(text: string, maxChars: number) {
  const normalized = text.replace(/\s+/g, " ").trim();
  if (normalized.length <= maxChars) return normalized;
  return `${normalized.slice(0, maxChars)}…`;
}

export function DashboardAnnouncementsClient({
  initialItems
}: DashboardAnnouncementsClientProps) {
  const { locale, t } = useI18n();
  const announcements = initialItems;
  const [openId, setOpenId] = React.useState<string | null>(null);
  const active = openId ? announcements.find((a) => a.id === openId) ?? null : null;

  return (
    <>
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
              <button
                key={a.id}
                type="button"
                onClick={() => setOpenId(a.id)}
                className={cn(
                  "w-full text-left rounded-xl border border-border bg-background/35 p-3",
                  "transition-all duration-300 hover:shadow-lg hover:scale-[1.01]"
                )}
              >
                <div className="text-sm font-medium text-foreground">{getAnnouncementTitle(a, locale)}</div>
                <div className="mt-1 font-mono tabular-nums text-xs text-muted-foreground">
                  <ClientDateTime value={a.createdAt} locale={locale} />
                </div>
                <div className="mt-2 text-sm text-muted-foreground">
                  {preview(getAnnouncementContent(a, locale), 120)}
                </div>
              </button>
            ))}
          </div>
        )}
      </CardContent>
    </Card>

      <Dialog
        open={openId !== null}
        onOpenChange={(next) => {
          if (!next) setOpenId(null);
        }}
      >
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{active ? getAnnouncementTitle(active, locale) : t("dashboard.ann.title")}</DialogTitle>
            <DialogDescription>
              {active ? <ClientDateTime value={active.createdAt} locale={locale} /> : null}
            </DialogDescription>
          </DialogHeader>
          {active ? (
            <div className="max-h-[60vh] overflow-auto whitespace-pre-wrap text-sm text-foreground">
              {getAnnouncementContent(active, locale)}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}
