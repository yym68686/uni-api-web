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
  if (!value) return "/";
  if (!value.startsWith("/")) return "/";
  if (value.startsWith("//")) return "/";
  return value;
}

const OAUTH_STATE_COOKIE = "uai_oauth_state";
const OAUTH_VERIFIER_COOKIE = "uai_oauth_verifier";
const OAUTH_NEXT_COOKIE = "uai_oauth_next";

export function GET(req: Request) {
  const url = new URL(req.url);
  const nextPath = sanitizeNextPath(url.searchParams.get("next"));

  const clientId = process.env.GOOGLE_CLIENT_ID ?? "";
  const redirectUri =
    process.env.GOOGLE_REDIRECT_URI ?? "http://localhost:3000/api/auth/google/callback";
  if (!clientId) {
    return NextResponse.json({ message: "Google OAuth not configured" }, { status: 500 });
  }

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
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: 60 * 10
  };
  res.cookies.set(OAUTH_STATE_COOKIE, state, cookieOptions);
  res.cookies.set(OAUTH_VERIFIER_COOKIE, verifier, cookieOptions);
  res.cookies.set(OAUTH_NEXT_COOKIE, nextPath, cookieOptions);
  return res;
}

