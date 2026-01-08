import { proxyToBackend } from "@/lib/proxy";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function GET(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  return proxyToBackend(req, `/keys/${encodeURIComponent(id)}/reveal`);
}

