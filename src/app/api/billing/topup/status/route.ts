import { revalidateTag } from "next/cache";

import { CACHE_TAGS } from "@/lib/cache-tags";
import { proxyToBackend } from "@/lib/proxy";

function isCompletedStatus(value: unknown): value is { status: "completed" } {
  if (!value || typeof value !== "object") return false;
  return (value as Record<string, unknown>).status === "completed";
}

export async function GET(req: Request) {
  const url = new URL(req.url);
  const qs = url.searchParams.toString();
  const path = qs.length > 0 ? `/billing/topup/status?${qs}` : "/billing/topup/status";

  const res = await proxyToBackend(req, path);
  if (!res.ok) return res;

  const json: unknown = await res.clone().json().catch(() => null);
  if (isCompletedStatus(json)) {
    revalidateTag(CACHE_TAGS.currentUser, { expire: 0 });
    revalidateTag(CACHE_TAGS.billingLedger, { expire: 0 });
  }
  return res;
}

