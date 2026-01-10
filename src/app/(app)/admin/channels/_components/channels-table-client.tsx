"use client";

import * as React from "react";
import { PlugZap } from "lucide-react";

import { ChannelRowActions } from "@/components/admin/channel-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { LlmChannelItem } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { UI_EVENTS } from "@/lib/ui-events";

function isChannelItem(value: unknown): value is LlmChannelItem {
  if (!value || typeof value !== "object") return false;
  const v = value as Partial<LlmChannelItem>;
  return typeof v.id === "string" && typeof v.name === "string" && typeof v.baseUrl === "string";
}

function badgeForGroup(group: string) {
  if (group === "default") return "default";
  if (group === "admin") return "warning";
  return "secondary";
}

interface AdminChannelsTableClientProps {
  initialItems: LlmChannelItem[];
}

export function AdminChannelsTableClient({ initialItems }: AdminChannelsTableClientProps) {
  const { locale, t } = useI18n();
  const [items, setItems] = React.useState<LlmChannelItem[]>(initialItems);

  function upsert(next: LlmChannelItem) {
    setItems((prev) => {
      const idx = prev.findIndex((c) => c.id === next.id);
      if (idx === -1) return [next, ...prev];
      const copy = [...prev];
      copy[idx] = next;
      return copy;
    });
  }

  function remove(id: string) {
    setItems((prev) => prev.filter((c) => c.id !== id));
  }

  React.useEffect(() => {
    function onCreated(event: Event) {
      if (!(event instanceof CustomEvent)) return;
      const detail: unknown = event.detail;
      if (!isChannelItem(detail)) return;
      upsert(detail);
    }
    window.addEventListener(UI_EVENTS.adminChannelsCreated, onCreated);
    return () => window.removeEventListener(UI_EVENTS.adminChannelsCreated, onCreated);
  }, []);

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState
            icon={<PlugZap className="h-6 w-6 text-muted-foreground uai-float-sm" />}
            title={t("admin.channels.empty")}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("admin.channels.table.name")}</TableHead>
                <TableHead>{t("admin.channels.table.baseUrl")}</TableHead>
                <TableHead>{t("admin.channels.table.apiKey")}</TableHead>
                <TableHead>{t("admin.channels.table.allowGroups")}</TableHead>
                <TableHead>{t("admin.channels.table.updated")}</TableHead>
                <TableHead className="w-12 text-right">{t("keys.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="font-medium text-foreground">{c.name}</TableCell>
                  <TableCell className="max-w-[360px] truncate font-mono text-xs text-muted-foreground">
                    {c.baseUrl}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{c.apiKeyMasked}</TableCell>
                  <TableCell>
                    {c.allowGroups.length === 0 ? (
                      <Badge variant="outline">{t("admin.channels.table.all")}</Badge>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {c.allowGroups.slice(0, 4).map((g) => (
                          <Badge key={g} variant={badgeForGroup(g)} className="font-mono">
                            {g}
                          </Badge>
                        ))}
                        {c.allowGroups.length > 4 ? (
                          <Badge variant="outline">+{c.allowGroups.length - 4}</Badge>
                        ) : null}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    <ClientDateTime value={c.updatedAt} locale={locale} />
                  </TableCell>
                  <TableCell className="p-2 text-right">
                    <ChannelRowActions channel={c} onUpdated={upsert} onDeleted={remove} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
    </Card>
  );
}
