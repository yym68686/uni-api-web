import { revalidateTag } from "next/cache";

import { CACHE_TAGS } from "@/lib/cache-tags";
import { proxyToBackend } from "@/lib/proxy";

interface RouteContext {
  params: Promise<{ prefix: string[] }>;
}

function encodePrefix(parts: string[]) {
  return parts.map((part) => encodeURIComponent(part)).join("/");
}

function revalidateModelPricing() {
  revalidateTag(CACHE_TAGS.modelPricingAdminConfig, { expire: 0 });
  revalidateTag(CACHE_TAGS.modelsAdminConfig, { expire: 0 });
  revalidateTag(CACHE_TAGS.modelsUser, { expire: 0 });
  revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { prefix } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/model-pricing/${encodePrefix(prefix)}`);
  if (res.ok) revalidateModelPricing();
  return res;
}

export async function DELETE(req: Request, ctx: RouteContext) {
  const { prefix } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/model-pricing/${encodePrefix(prefix)}`);
  if (res.ok) revalidateModelPricing();
  return res;
}
