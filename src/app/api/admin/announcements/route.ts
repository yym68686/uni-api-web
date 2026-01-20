import { proxyToBackend } from "@/lib/proxy";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { revalidateTag } from "next/cache";

export function GET(req: Request) {
  return proxyToBackend(req, "/announcements");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/announcements");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.announcements, { expire: 0 });
    revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
  }
  return res;
}
