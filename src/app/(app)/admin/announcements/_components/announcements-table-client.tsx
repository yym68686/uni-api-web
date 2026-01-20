"use client";

import * as React from "react";
import { Megaphone } from "lucide-react";

import { AnnouncementRowActions } from "@/components/admin/announcement-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AnnouncementItem } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { UI_EVENTS } from "@/lib/ui-events";

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
    window.addEventListener(UI_EVENTS.adminAnnouncementsCreated, onCreated);
    return () => window.removeEventListener(UI_EVENTS.adminAnnouncementsCreated, onCreated);
  }, []);

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState
            icon={(
              <span className="inline-flex uai-float-sm">
                <Megaphone className="h-6 w-6 text-muted-foreground" />
              </span>
            )}
            title={t("admin.ann.empty")}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table variant="card">
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
                <TableRow key={a.id} className="uai-cv-auto">
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
                    <TableCell className="text-right">
                      <AnnouncementRowActions announcement={a} onUpdated={upsert} onDeleted={remove} />
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
    </Card>
  );
}
