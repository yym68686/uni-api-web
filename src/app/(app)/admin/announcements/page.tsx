import { Megaphone } from "lucide-react";

import { AnnouncementPublisher } from "@/components/admin/announcement-publisher";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import type { AnnouncementsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
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

async function getAnnouncements() {
  const res = await fetch(buildBackendUrl("/announcements"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAnnouncementsListResponse(json)) return null;
  return json.items;
}

export default async function AdminAnnouncementsPage() {
  const me = await getMe();
  const items = (await getAnnouncements()) ?? [];

  const isAdmin = me?.role === "admin";

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Announcements</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            仅管理员可发布公告（当前：{me?.email ?? "unknown"}）。
          </p>
        </div>
        {isAdmin ? <AnnouncementPublisher /> : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Megaphone className="h-5 w-5 text-muted-foreground" />
              Forbidden
            </CardTitle>
            <CardDescription>你不是管理员，无法发布公告。</CardDescription>
          </CardHeader>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Recent</CardTitle>
          <CardDescription>最近展示在 Dashboard 的公告</CardDescription>
        </CardHeader>
        <CardContent>
          {items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              暂无公告
            </div>
          ) : (
            <div className="space-y-3">
              {items.map((a) => (
                <div key={a.id} className="rounded-xl border border-border bg-card p-4">
                  <div className="text-sm font-medium text-foreground">{a.title}</div>
                  <div className="mt-1 text-xs text-muted-foreground font-mono">{a.meta}</div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
