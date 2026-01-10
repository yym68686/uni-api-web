import { AdminChannelsTableClient } from "./channels-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { LlmChannelsListResponse } from "@/lib/types";

function isLlmChannelsListResponse(value: unknown): value is LlmChannelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getChannels() {
  const res = await fetch(buildBackendUrl("/admin/channels"), {
    cache: "force-cache",
    next: { tags: ["admin:channels", "models:admin-config", "models:user"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isLlmChannelsListResponse(json)) return null;
  return json.items;
}

interface AdminChannelsContentProps {
  locale: Locale;
}

export async function AdminChannelsContent({ locale }: AdminChannelsContentProps) {
  const items = (await getChannels()) ?? [];

  void locale;
  return <AdminChannelsTableClient initialItems={items} />;
}
