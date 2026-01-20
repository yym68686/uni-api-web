import { proxyToBackend } from "@/lib/proxy";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { revalidateTag } from "next/cache";

export async function GET(req: Request) {
  return proxyToBackend(req, "/admin/settings");
}

export async function PATCH(req: Request) {
  const res = await proxyToBackend(req, "/admin/settings");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.adminSettings, { expire: 0 });
    revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
  }
  return res;
}
