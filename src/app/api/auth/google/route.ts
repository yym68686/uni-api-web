import crypto from "node:crypto";

import { NextResponse } from "next/server";

function base64Url(input: Buffer) {
  return input
    .toString("base64")
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

function sha256Base64Url(input: string) {
  const hash = crypto.createHash("sha256").update(input).digest();
  return base64Url(hash);
}

function sanitizeNextPath(value: string | null) {
  if (!value) return "/dashboard";
  if (!value.startsWith("/")) return "/dashboard";
  if (value.startsWith("//")) return "/dashboard";
  return value;
}

function sanitizeFromPath(value: string | null) {
  if (value === "/login") return "/login";
  if (value === "/register") return "/register";
  return "/login";
}

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

const OAUTH_STATE_COOKIE = "uai_oauth_state";
const OAUTH_VERIFIER_COOKIE = "uai_oauth_verifier";
const OAUTH_NEXT_COOKIE = "uai_oauth_next";
const OAUTH_FROM_COOKIE = "uai_oauth_from";

export function GET(req: Request) {
  const url = new URL(req.url);
  const nextPath = sanitizeNextPath(url.searchParams.get("next"));
  const fromPath = sanitizeFromPath(url.searchParams.get("from"));

  const clientId = process.env.GOOGLE_CLIENT_ID ?? "";
  const publicOrigin = getPublicOrigin(req, url);
  const redirectUri =
    process.env.GOOGLE_REDIRECT_URI ??
    new URL("/api/auth/google/callback", publicOrigin).toString();
  if (!clientId) {
    return NextResponse.json({ message: "Google OAuth not configured" }, { status: 500 });
  }

  const isSecureRequest =
    req.headers.get("x-forwarded-proto") === "https" || url.protocol === "https:";

  const state = base64Url(crypto.randomBytes(18));
  const verifier = base64Url(crypto.randomBytes(32));
  const challenge = sha256Base64Url(verifier);

  const authUrl = new URL("https://accounts.google.com/o/oauth2/v2/auth");
  authUrl.searchParams.set("client_id", clientId);
  authUrl.searchParams.set("redirect_uri", redirectUri);
  authUrl.searchParams.set("response_type", "code");
  authUrl.searchParams.set("scope", "openid email profile");
  authUrl.searchParams.set("state", state);
  authUrl.searchParams.set("code_challenge", challenge);
  authUrl.searchParams.set("code_challenge_method", "S256");
  authUrl.searchParams.set("prompt", "select_account");

  const res = NextResponse.redirect(authUrl);
  const cookieOptions = {
    httpOnly: true,
    sameSite: "lax" as const,
    secure: isSecureRequest,
    path: "/",
    maxAge: 60 * 10
  };
  res.cookies.set(OAUTH_STATE_COOKIE, state, cookieOptions);
  res.cookies.set(OAUTH_VERIFIER_COOKIE, verifier, cookieOptions);
  res.cookies.set(OAUTH_NEXT_COOKIE, nextPath, cookieOptions);
  res.cookies.set(OAUTH_FROM_COOKIE, fromPath, cookieOptions);
  return res;
}
