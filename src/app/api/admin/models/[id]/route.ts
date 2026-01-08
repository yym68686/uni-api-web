import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/models/${encodeURIComponent(id)}`);
  if (res.ok) {
    revalidateTag("models:admin-config", { expire: 0 });
    revalidateTag("models:user", { expire: 0 });
  }
  return res;
}
