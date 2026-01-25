import { revalidateTag } from "next/cache";

import { CACHE_TAGS } from "@/lib/cache-tags";
import { proxyToBackend } from "@/lib/proxy";

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/auth/email/change/confirm");
  if (res.ok) {
    revalidateTag(CACHE_TAGS.currentUser, { expire: 0 });
  }
  return res;
}

