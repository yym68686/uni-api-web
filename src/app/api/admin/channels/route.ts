import { proxyToBackend } from "@/lib/proxy";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { revalidateTag } from "next/cache";

export function GET(req: Request) {
  return proxyToBackend(req, "/admin/channels");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/channels");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.adminChannels, { expire: 0 });
    revalidateTag(CACHE_TAGS.modelsAdminConfig, { expire: 0 });
    revalidateTag(CACHE_TAGS.modelsUser, { expire: 0 });
  }
  return res;
}
