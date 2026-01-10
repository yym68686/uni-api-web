import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

export async function GET(req: Request) {
  return proxyToBackend(req, "/admin/settings");
}

export async function PATCH(req: Request) {
  const res = await proxyToBackend(req, "/admin/settings");
  if (res.ok) revalidateTag("admin:settings", { expire: 0 });
  return res;
}
