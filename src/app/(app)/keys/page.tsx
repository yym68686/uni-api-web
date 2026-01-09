import { ApiKeysPageClient } from "@/components/keys/api-keys-page-client";
import type { ApiKeysListResponse } from "@/lib/types";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";

export const dynamic = "force-dynamic";

function isApiKeysListResponse(value: unknown): value is ApiKeysListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

export default async function ApiKeysPage() {
  let items: ApiKeysListResponse["items"] = [];
  try {
    const res = await fetch(buildBackendUrl("/keys"), {
      cache: "no-store",
      headers: await getBackendAuthHeadersCached()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (isApiKeysListResponse(json)) items = json.items;
    }
  } catch {
    // Backend not available; keep page resilient.
  }
  return <ApiKeysPageClient initialItems={items} />;
}
