import { AdminModelPricingTableClient } from "./model-pricing-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { AdminModelPricingListResponse } from "@/lib/types";

function isAdminModelPricingListResponse(value: unknown): value is AdminModelPricingListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getModelPricingRules() {
  const res = await fetch(buildBackendUrl("/admin/model-pricing"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.modelPricingAdminConfig] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelPricingListResponse(json)) return null;
  return json.items;
}

export async function AdminModelPricingContent() {
  const items = (await getModelPricingRules()) ?? [];

  return <AdminModelPricingTableClient initialItems={items} />;
}
