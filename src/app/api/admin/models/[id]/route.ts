import { proxyToBackend } from "@/lib/proxy";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  return proxyToBackend(req, `/admin/models/${encodeURIComponent(id)}`);
}

