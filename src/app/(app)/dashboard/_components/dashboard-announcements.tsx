import { Megaphone } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ClientDateTime } from "@/components/common/client-datetime";
import { cn } from "@/lib/utils";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { getDashboardAnnouncements } from "./dashboard-data";

interface DashboardAnnouncementsProps {
  locale: Locale;
}

export async function DashboardAnnouncements({ locale }: DashboardAnnouncementsProps) {
  const announcements = await getDashboardAnnouncements();

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
