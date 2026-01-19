import { ApiKeysPageClient } from "@/components/keys/api-keys-page-client";
import { getPublicApiBaseUrl } from "@/lib/app-config";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { ApiKeysListResponse } from "@/lib/types";

function isApiKeysListResponse(value: unknown): value is ApiKeysListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

export async function KeysContent() {
  const publicApiBaseUrl = getPublicApiBaseUrl();
  let items: ApiKeysListResponse["items"] = [];
  try {
    const res = await fetch(buildBackendUrl("/keys"), {
      cache: "force-cache",
      next: { tags: ["keys:user"] },
      headers: await getBackendAuthHeadersCached()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (isApiKeysListResponse(json)) items = json.items;
    }
  } catch {
    // Backend not available; keep page resilient.
  }

  return <ApiKeysPageClient initialItems={items} publicApiBaseUrl={publicApiBaseUrl} />;
}
