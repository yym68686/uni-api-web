import { Badge } from "@/components/ui/badge";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { AnnouncementsListResponse } from "@/lib/types";
import { AnnouncementRowActions } from "@/components/admin/announcement-row-actions";

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getAnnouncements() {
  const res = await fetch(buildBackendUrl("/announcements"), {
    cache: "force-cache",
    next: { tags: ["announcements"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAnnouncementsListResponse(json)) return null;
  return json.items;
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

interface AdminAnnouncementsContentProps {
  locale: Locale;
  isAdmin: boolean;
}

export async function AdminAnnouncementsContent({ locale, isAdmin }: AdminAnnouncementsContentProps) {
  const items = (await getAnnouncements()) ?? [];

  return (
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
                <TableHead>{t(locale, "admin.ann.table.title")}</TableHead>
                <TableHead>{t(locale, "admin.ann.table.meta")}</TableHead>
                <TableHead>{t(locale, "admin.ann.table.level")}</TableHead>
                <TableHead>{t(locale, "admin.ann.table.created")}</TableHead>
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
                    <ClientDateTime value={a.createdAt} locale={locale} />
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
  );
}
