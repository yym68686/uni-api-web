import { Badge } from "@/components/ui/badge";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { LlmChannelsListResponse } from "@/lib/types";
import { ChannelRowActions } from "@/components/admin/channel-row-actions";

function isLlmChannelsListResponse(value: unknown): value is LlmChannelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getChannels() {
  const res = await fetch(buildBackendUrl("/admin/channels"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isLlmChannelsListResponse(json)) return null;
  return json.items;
}

function badgeForGroup(group: string) {
  if (group === "default") return "default";
  if (group === "admin") return "warning";
  return "secondary";
}

interface AdminChannelsContentProps {
  locale: Locale;
}

export async function AdminChannelsContent({ locale }: AdminChannelsContentProps) {
  const items = (await getChannels()) ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(locale, "admin.channels.card.title")}</CardTitle>
        <CardDescription>{t(locale, "admin.channels.card.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
            {t(locale, "admin.channels.empty")}
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(locale, "admin.channels.table.name")}</TableHead>
                <TableHead>{t(locale, "admin.channels.table.baseUrl")}</TableHead>
                <TableHead>{t(locale, "admin.channels.table.apiKey")}</TableHead>
                <TableHead>{t(locale, "admin.channels.table.allowGroups")}</TableHead>
                <TableHead>{t(locale, "admin.channels.table.updated")}</TableHead>
                <TableHead className="w-12 text-right">{t(locale, "keys.table.actions")}</TableHead>
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
                      <Badge variant="outline">{t(locale, "admin.channels.table.all")}</Badge>
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
                    <ChannelRowActions channel={c} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
