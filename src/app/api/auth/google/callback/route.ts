import { NextResponse } from "next/server";
import { cookies } from "next/headers";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";

const OAUTH_STATE_COOKIE = "uai_oauth_state";
const OAUTH_VERIFIER_COOKIE = "uai_oauth_verifier";
const OAUTH_NEXT_COOKIE = "uai_oauth_next";

function firstHeader(req: Request, name: string) {
  const raw = req.headers.get(name);
  if (!raw) return null;
  const first = raw.split(",")[0]?.trim();
  return first && first.length > 0 ? first : null;
}

function getPublicOrigin(req: Request, url: URL) {
  const forwardedProto = firstHeader(req, "x-forwarded-proto");
  const forwardedHost = firstHeader(req, "x-forwarded-host");
  const proto = forwardedProto ?? url.protocol.replace(":", "");
  const host = forwardedHost ?? req.headers.get("host");
  if (!host) return url.origin;
  return `${proto}://${host}`;
}

function sanitizeNextPath(value: string | null) {
  if (!value) return "/dashboard";
  if (!value.startsWith("/")) return "/dashboard";
  if (value.startsWith("//")) return "/dashboard";
  return value;
}

interface UpstreamAuthResponse {
  token: string;
  user: { id: string; email: string };
}

function isUpstreamAuthResponse(value: unknown): value is UpstreamAuthResponse {
  if (!value || typeof value !== "object") return false;
  if (!("token" in value) || !("user" in value)) return false;
  const token = (value as { token?: unknown }).token;
  return typeof token === "string" && token.length > 10;
}

export async function GET(req: Request) {
  const url = new URL(req.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const publicOrigin = getPublicOrigin(req, url);

  const store = await cookies();
  const savedState = store.get(OAUTH_STATE_COOKIE)?.value ?? null;
  const verifier = store.get(OAUTH_VERIFIER_COOKIE)?.value ?? null;
  const nextPath = sanitizeNextPath(store.get(OAUTH_NEXT_COOKIE)?.value ?? "/dashboard");

  const clear = NextResponse.redirect(new URL("/login", publicOrigin));
  clear.cookies.set(OAUTH_STATE_COOKIE, "", { path: "/", maxAge: 0 });
  clear.cookies.set(OAUTH_VERIFIER_COOKIE, "", { path: "/", maxAge: 0 });
  clear.cookies.set(OAUTH_NEXT_COOKIE, "", { path: "/", maxAge: 0 });

  if (!code || !state || !savedState || state !== savedState || !verifier) {
    return clear;
  }

  const redirectUri =
    process.env.GOOGLE_REDIRECT_URI ??
    new URL("/api/auth/google/callback", publicOrigin).toString();

  const upstream = await fetch(buildBackendUrl("/auth/oauth/google"), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ code, codeVerifier: verifier, redirectUri }),
    cache: "no-store"
  }).catch(() => null);

  if (!upstream) return clear;
  const json: unknown = await upstream.json().catch(() => null);
  if (!upstream.ok || !isUpstreamAuthResponse(json)) return clear;

  const isSecureRequest =
    req.headers.get("x-forwarded-proto") === "https" || url.protocol === "https:";

  const res = NextResponse.redirect(new URL(nextPath, publicOrigin));
  res.cookies.set(SESSION_COOKIE_NAME, json.token, {
    httpOnly: true,
    sameSite: "lax",
    secure: isSecureRequest,
    path: "/",
    maxAge: 60 * 60 * 24 * 7
  });
  res.cookies.set(OAUTH_STATE_COOKIE, "", { path: "/", maxAge: 0 });
  res.cookies.set(OAUTH_VERIFIER_COOKIE, "", { path: "/", maxAge: 0 });
  res.cookies.set(OAUTH_NEXT_COOKIE, "", { path: "/", maxAge: 0 });
  return res;
}
