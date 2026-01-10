import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

export function GET(req: Request) {
  return proxyToBackend(req, "/admin/channels");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/channels");
  if (res.ok) {
    revalidateTag("admin:channels", { expire: 0 });
    revalidateTag("models:admin-config", { expire: 0 });
    revalidateTag("models:user", { expire: 0 });
  }
  return res;
}
