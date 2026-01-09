import { Megaphone } from "lucide-react";

import { AnnouncementPublisher } from "@/components/admin/announcement-publisher";
import { AnnouncementRowActions } from "@/components/admin/announcement-row-actions";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { getCurrentUser } from "@/lib/current-user";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import type { AnnouncementsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getAnnouncements() {
  const res = await fetch(buildBackendUrl("/announcements"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAnnouncementsListResponse(json)) return null;
  return json.items;
}

function formatCreatedAt(locale: string, createdAt: string) {
  const dt = new Date(createdAt);
  if (Number.isNaN(dt.getTime())) return createdAt;
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(dt);
}

function levelBadgeVariant(level: string) {
  switch (level) {
    case "success":
      return "success";
    case "warning":
      return "warning";
    case "destructive":
      return "destructive";
    default:
      return "default";
  }
}

export default async function AdminAnnouncementsPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const items = (await getAnnouncements()) ?? [];

  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.announcements")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(locale, "admin.ann.subtitle", { current })}
          </p>
        </div>
        {isAdmin ? <AnnouncementPublisher /> : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Megaphone className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.ann.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>{t(locale, "admin.ann.recent")}</CardTitle>
          <CardDescription>{t(locale, "admin.ann.recentDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          {items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              {t(locale, "admin.ann.empty")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Meta</TableHead>
                  <TableHead>Level</TableHead>
                  <TableHead>Created</TableHead>
                  {isAdmin ? <TableHead className="w-12 text-right">{t(locale, "keys.table.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell className="font-medium text-foreground">{a.title}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{a.meta}</TableCell>
                    <TableCell>
                      <Badge variant={levelBadgeVariant(a.level)} className="capitalize">
                        {a.level}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatCreatedAt(locale, a.createdAt)}
                    </TableCell>
                    {isAdmin ? (
                      <TableCell className="p-2 text-right">
                        <AnnouncementRowActions announcement={a} />
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
