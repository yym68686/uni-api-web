import { revalidateTag } from "next/cache";

import { proxyToBackend } from "@/lib/proxy";
import { CACHE_TAGS } from "@/lib/cache-tags";

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/auth/password/set");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.authMethods, { expire: 0 });
    revalidateTag(CACHE_TAGS.currentUser, { expire: 0 });
  }
  return res;
}

