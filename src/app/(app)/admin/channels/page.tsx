import { PlugZap } from "lucide-react";

import { ChannelPublisher } from "@/components/admin/channel-publisher";
import { ChannelRowActions } from "@/components/admin/channel-row-actions";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import type { LlmChannelsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isLlmChannelsListResponse(value: unknown): value is LlmChannelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatDateTime(value: string) {
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return value;
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(dt);
}

async function getMe() {
  const res = await fetch(buildBackendUrl("/auth/me"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!json || typeof json !== "object") return null;
  const role = (json as { role?: unknown }).role;
  const email = (json as { email?: unknown }).email;
  if (typeof role !== "string" || typeof email !== "string") return null;
  return { role, email };
}

async function getChannels() {
  const res = await fetch(buildBackendUrl("/admin/channels"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
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

export default async function AdminChannelsPage() {
  const me = await getMe();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const items = isAdmin ? (await getChannels()) ?? [] : [];

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Channels</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            管理 LLM API 渠道（当前：{me?.email ?? "unknown"}）。
          </p>
        </div>
        {isAdmin ? <ChannelPublisher /> : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <PlugZap className="h-5 w-5 text-muted-foreground" />
              Forbidden
            </CardTitle>
            <CardDescription>你不是管理员，无法管理渠道。</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Configured channels</CardTitle>
            <CardDescription>渠道可限制哪些分组（group）可用；留空表示所有分组都可用。</CardDescription>
          </CardHeader>
          <CardContent>
            {items.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
                暂无渠道
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Base URL</TableHead>
                    <TableHead>API key</TableHead>
                    <TableHead>Allow groups</TableHead>
                    <TableHead>Updated</TableHead>
                    <TableHead className="w-12 text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((c) => (
                    <TableRow key={c.id}>
                      <TableCell className="font-medium text-foreground">{c.name}</TableCell>
                      <TableCell className="max-w-[360px] truncate font-mono text-xs text-muted-foreground">
                        {c.baseUrl}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {c.apiKeyMasked}
                      </TableCell>
                      <TableCell>
                        {c.allowGroups.length === 0 ? (
                          <Badge variant="outline">All</Badge>
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
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatDateTime(c.updatedAt)}
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
      )}
    </div>
  );
}

