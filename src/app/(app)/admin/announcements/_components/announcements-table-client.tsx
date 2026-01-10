"use client";

import * as React from "react";

import { AnnouncementRowActions } from "@/components/admin/announcement-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AnnouncementItem } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";

const ANNOUNCEMENT_CREATED_EVENT = "uai:admin:announcements:created";

function isAnnouncementItem(value: unknown): value is AnnouncementItem {
  if (!value || typeof value !== "object") return false;
  const v = value as Partial<AnnouncementItem>;
  return typeof v.id === "string" && typeof v.title === "string" && typeof v.meta === "string";
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

interface AdminAnnouncementsTableClientProps {
  initialItems: AnnouncementItem[];
  canManage: boolean;
}

export function AdminAnnouncementsTableClient({ initialItems, canManage }: AdminAnnouncementsTableClientProps) {
  const { locale, t } = useI18n();
  const [items, setItems] = React.useState<AnnouncementItem[]>(initialItems);

  function upsert(next: AnnouncementItem) {
    setItems((prev) => {
      const idx = prev.findIndex((a) => a.id === next.id);
      if (idx === -1) return [next, ...prev];
      const copy = [...prev];
      copy[idx] = next;
      return copy;
    });
  }

  function remove(id: string) {
    setItems((prev) => prev.filter((a) => a.id !== id));
  }

  React.useEffect(() => {
    function onCreated(event: Event) {
      if (!(event instanceof CustomEvent)) return;
      const detail: unknown = event.detail;
      if (!isAnnouncementItem(detail)) return;
      upsert(detail);
    }
    window.addEventListener(ANNOUNCEMENT_CREATED_EVENT, onCreated);
    return () => window.removeEventListener(ANNOUNCEMENT_CREATED_EVENT, onCreated);
  }, []);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("admin.ann.recent")}</CardTitle>
        <CardDescription>{t("admin.ann.recentDesc")}</CardDescription>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
            {t("admin.ann.empty")}
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("admin.ann.table.title")}</TableHead>
                <TableHead>{t("admin.ann.table.meta")}</TableHead>
                <TableHead>{t("admin.ann.table.level")}</TableHead>
                <TableHead>{t("admin.ann.table.created")}</TableHead>
                {canManage ? <TableHead className="w-12 text-right">{t("keys.table.actions")}</TableHead> : null}
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
                  {canManage ? (
                    <TableCell className="p-2 text-right">
                      <AnnouncementRowActions announcement={a} onUpdated={upsert} onDeleted={remove} />
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

