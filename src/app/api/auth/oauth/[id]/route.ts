import { revalidateTag } from "next/cache";

import { CACHE_TAGS } from "@/lib/cache-tags";
import { proxyToBackend } from "@/lib/proxy";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function DELETE(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const path = `/auth/oauth/${encodeURIComponent(id)}`;
  const res = await proxyToBackend(req, path);
  if (res.ok) {
    revalidateTag(CACHE_TAGS.authMethods, { expire: 0 });
  }
  return res;
}

