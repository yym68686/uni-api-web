import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/channels/${encodeURIComponent(id)}`);
  if (res.ok) {
    revalidateTag("admin:channels", { expire: 0 });
    revalidateTag("models:admin-config", { expire: 0 });
    revalidateTag("models:user", { expire: 0 });
  }
  return res;
}

export async function DELETE(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/channels/${encodeURIComponent(id)}`);
  if (res.ok) {
    revalidateTag("admin:channels", { expire: 0 });
    revalidateTag("models:admin-config", { expire: 0 });
    revalidateTag("models:user", { expire: 0 });
  }
  return res;
}
