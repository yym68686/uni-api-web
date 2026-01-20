import { NextResponse } from "next/server";
import { revalidateTag } from "next/cache";
import { cookies } from "next/headers";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";

interface MeResponse {
  role?: unknown;
}

export async function POST() {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  if (!token) return NextResponse.json({ message: "Unauthorized" }, { status: 401 });

  const meRes = await fetch(buildBackendUrl("/auth/me"), {
    cache: "no-store",
    headers: { authorization: `Bearer ${token}` }
  }).catch(() => null);
  if (!meRes || !meRes.ok) return NextResponse.json({ message: "Unauthorized" }, { status: 401 });

  const me: unknown = await meRes.json().catch(() => null);
  const role = (me as MeResponse | null)?.role;
  if (role !== "admin" && role !== "owner") {
    return NextResponse.json({ message: "Forbidden" }, { status: 403 });
  }

  revalidateTag(CACHE_TAGS.modelsAdminConfig, { expire: 0 });
  revalidateTag(CACHE_TAGS.modelsUser, { expire: 0 });
  revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
  return NextResponse.json({ ok: true });
}
