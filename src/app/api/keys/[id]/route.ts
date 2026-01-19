import { proxyToBackend } from "@/lib/proxy";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { revalidateTag } from "next/cache";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function DELETE(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/keys/${id}`);
  if (res.ok) revalidateTag(CACHE_TAGS.keysUser, { expire: 0 });
  return res;
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/keys/${id}`);
  if (res.ok) revalidateTag(CACHE_TAGS.keysUser, { expire: 0 });
  return res;
}
