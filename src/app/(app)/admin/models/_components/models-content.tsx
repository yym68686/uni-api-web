import { AdminModelsTableClient } from "./models-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { AdminModelsListResponse } from "@/lib/types";

function isAdminModelsListResponse(value: unknown): value is AdminModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getModels() {
  const res = await fetch(buildBackendUrl("/admin/models"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.modelsAdminConfig, CACHE_TAGS.modelsUser] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelsListResponse(json)) return null;
  return json.items;
}

export async function AdminModelsContent() {
  const items = (await getModels()) ?? [];

  return <AdminModelsTableClient initialItems={items} />;
}
