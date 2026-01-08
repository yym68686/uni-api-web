import { NextResponse } from "next/server";
import { cookies } from "next/headers";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";

export async function POST() {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
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
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: 0
  });
  return res;
}
