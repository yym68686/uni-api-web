import { NextResponse } from "next/server";
import { cookies } from "next/headers";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";

export async function POST(req: Request) {
  const url = new URL(req.url);
  const isSecureRequest =
    req.headers.get("x-forwarded-proto") === "https" || url.protocol === "https:";

  const store = await cookies();
  const token = store.get(SESSION_COOKIE_NAME)?.value;
  if (token) {
    await fetch(buildBackendUrl("/auth/logout"), {
      method: "POST",
      headers: { authorization: `Bearer ${token}` },
      cache: "no-store"
    }).catch(() => null);
  }

  const res = NextResponse.json({ ok: true });
  res.cookies.set(SESSION_COOKIE_NAME, "", {
    httpOnly: true,
    sameSite: "lax",
    secure: isSecureRequest,
    path: "/",
    maxAge: 0
  });
  return res;
}
