"use client";

import { Megaphone } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { API_PATHS } from "@/lib/api-paths";
import { cn } from "@/lib/utils";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { AnnouncementsListResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function fetchAnnouncements() {
  const res = await fetch(API_PATHS.announcements, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isAnnouncementsListResponse(json)) throw new Error("Invalid response");
  return json.items;
}

interface DashboardAnnouncementsClientProps {
  locale: Locale;
  initialItems: AnnouncementsListResponse["items"];
}

export function DashboardAnnouncementsClient({
  locale,
  initialItems
}: DashboardAnnouncementsClientProps) {
  const { data } = useSwrLite<AnnouncementsListResponse["items"]>(API_PATHS.announcements, fetchAnnouncements, {
    fallbackData: initialItems,
    revalidateOnFocus: true
  });

  const announcements = data ?? initialItems;

  return (
    <Card className="bg-warning/10">
      <CardHeader>
        <CardTitle>{t(locale, "dashboard.ann.title")}</CardTitle>
        <CardDescription>{t(locale, "dashboard.ann.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        {announcements.length === 0 ? (
          <EmptyState
            className="bg-background/20 p-6"
            icon={<Megaphone className="h-6 w-6 text-muted-foreground uai-float-sm" />}
            title={t(locale, "dashboard.ann.empty")}
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
                <div className="text-sm font-medium text-foreground">{a.title}</div>
                <div className="mt-1 font-mono tabular-nums text-xs text-muted-foreground">
                  <ClientDateTime value={a.createdAt} locale={locale} /> · {a.meta}
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

