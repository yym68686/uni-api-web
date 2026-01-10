import { AdminAnnouncementsTableClient } from "./announcements-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { AnnouncementsListResponse } from "@/lib/types";

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

interface AdminAnnouncementsContentProps {
  locale: Locale;
  isAdmin: boolean;
}

export async function AdminAnnouncementsContent({ locale, isAdmin }: AdminAnnouncementsContentProps) {
  const items = (await getAnnouncements()) ?? [];

  void locale;
  return <AdminAnnouncementsTableClient initialItems={items} canManage={isAdmin} />;
}
