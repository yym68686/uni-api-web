import { NextResponse } from "next/server";
import { z } from "zod";
import { revalidateTag } from "next/cache";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";

const schema = z.object({
  email: z.string().trim().email(),
  password: z.string().min(6).max(128),
  inviteCode: z.string().trim().max(16).optional()
});

interface AuthResponseBody {
  token: string;
  user: { id: string; email: string };
}

function safeJsonParse(text: string): unknown | null {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

function isAuthResponseBody(value: unknown): value is AuthResponseBody {
  if (!value || typeof value !== "object") return false;
  if (!("token" in value) || !("user" in value)) return false;
  const token = (value as { token?: unknown }).token;
  return typeof token === "string" && token.length > 10;
}

export async function POST(req: Request) {
  const url = new URL(req.url);
  const isSecureRequest =
    req.headers.get("x-forwarded-proto") === "https" || url.protocol === "https:";

  const json: unknown = await req.json().catch(() => null);
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    return NextResponse.json(
      { message: "invalid_payload", issues: parsed.error.issues },
      { status: 400 }
    );
  }

  const headers: Record<string, string> = { "content-type": "application/json" };
  const cookie = req.headers.get("cookie");
  if (cookie) headers.cookie = cookie;
  const xff = req.headers.get("x-forwarded-for");
  if (xff) headers["x-forwarded-for"] = xff;
  const realIp = req.headers.get("x-real-ip");
  if (realIp) headers["x-real-ip"] = realIp;
  const ua = req.headers.get("user-agent");
  if (ua) headers["user-agent"] = ua;

  const upstream = await fetch(buildBackendUrl("/auth/register"), {
    method: "POST",
    headers,
    body: JSON.stringify(parsed.data),
    cache: "no-store"
  });

  const upstreamText = await upstream.text().catch(() => "");
  const upstreamJson: unknown = safeJsonParse(upstreamText);
  if (!upstream.ok) {
    let message = "register_failed";
    const detail = upstreamJson && typeof upstreamJson === "object" ? (upstreamJson as { detail?: unknown }).detail : undefined;
    if (typeof detail === "string") message = detail;
    return NextResponse.json({ message }, { status: upstream.status });
  }

  if (!isAuthResponseBody(upstreamJson)) {
    return NextResponse.json({ message: "invalid_upstream_response" }, { status: 502 });
  }

  const res = NextResponse.json({ ok: true, user: upstreamJson.user });
  res.cookies.set(SESSION_COOKIE_NAME, upstreamJson.token, {
    httpOnly: true,
    sameSite: "lax",
    secure: isSecureRequest,
    path: "/",
    maxAge: 60 * 60 * 24 * 7
  });
  revalidateTag(CACHE_TAGS.currentUser, { expire: 0 });
  revalidateTag(CACHE_TAGS.adminUsers, { expire: 0 });
  revalidateTag(CACHE_TAGS.adminOverview, { expire: 0 });
  return res;
}
