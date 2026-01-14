import { AdminChannelsTableClient } from "./channels-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
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

export async function AdminChannelsContent() {
  const items = (await getChannels()) ?? [];

  return <AdminChannelsTableClient initialItems={items} />;
}
