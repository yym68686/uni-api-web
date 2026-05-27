import { revalidateTag } from "next/cache";

import { CACHE_TAGS } from "@/lib/cache-tags";
import { proxyToBackend } from "@/lib/proxy";

export function GET(req: Request) {
  return proxyToBackend(req, "/admin/model-pricing");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/model-pricing");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.modelPricingAdminConfig, { expire: 0 });
    revalidateTag(CACHE_TAGS.modelsAdminConfig, { expire: 0 });
    revalidateTag(CACHE_TAGS.modelsUser, { expire: 0 });
    revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
  }
  return res;
}
