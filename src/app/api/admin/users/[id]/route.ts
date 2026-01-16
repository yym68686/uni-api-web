import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

interface RouteContext {
  params: Promise<{ id: string }>;
}

export async function PATCH(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/users/${encodeURIComponent(id)}`);
  if (res.ok) {
    revalidateTag("admin:users", { expire: 0 });
    revalidateTag("billing:ledger", { expire: 0 });
  }
  return res;
}

export async function DELETE(req: Request, ctx: RouteContext) {
  const { id } = await ctx.params;
  const res = await proxyToBackend(req, `/admin/users/${encodeURIComponent(id)}`);
  if (res.ok) {
    revalidateTag("admin:users", { expire: 0 });
    revalidateTag("billing:ledger", { expire: 0 });
  }
  return res;
}
