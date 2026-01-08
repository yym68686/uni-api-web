import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";

function normalizeBaseUrl(raw: string) {
  return raw.trim().replace(/\/+$/, "");
}

function buildBackendUrl(path: string) {
  const base = normalizeBaseUrl(process.env.API_BASE_URL ?? "http://localhost:8001/v1");
  const normalizedPath = path.replace(/^\/+/, "");
  return new URL(normalizedPath, `${base}/`).toString();
}

function sanitizeNextPath(value: string | null) {
  if (!value) return "/";
  if (!value.startsWith("/")) return "/";
  if (value.startsWith("//")) return "/";
  return value;
}

function clearSessionCookie(res: NextResponse) {
  res.cookies.set(SESSION_COOKIE_NAME, "", {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: 0
  });
  return res;
}

async function isSessionValid(token: string) {
  try {
    const res = await fetch(buildBackendUrl("/auth/me"), {
      headers: { authorization: `Bearer ${token}` },
      cache: "no-store"
    });
    return res.ok;
  } catch {
    return false;
  }
}

function isPublicPath(pathname: string) {
  if (pathname === "/login") return true;
  if (pathname === "/register") return true;
  if (pathname.startsWith("/api/auth")) return true;
  if (pathname.startsWith("/_next")) return true;
  if (pathname.startsWith("/favicon")) return true;
  if (pathname.startsWith("/robots")) return true;
  if (pathname.startsWith("/sitemap")) return true;
  return false;
}

function isProtectedPath(pathname: string) {
  if (pathname === "/") return true;
  if (pathname.startsWith("/keys")) return true;
  if (pathname.startsWith("/models")) return true;
  if (pathname.startsWith("/logs")) return true;
  if (pathname.startsWith("/profile")) return true;
  if (pathname.startsWith("/admin")) return true;
  if (pathname.startsWith("/api/keys")) return true;
  if (pathname.startsWith("/api/usage")) return true;
  if (pathname.startsWith("/api/admin")) return true;
  return false;
}

export async function proxy(req: NextRequest) {
  const { pathname, searchParams } = req.nextUrl;

  if (pathname === "/v1" || pathname.startsWith("/v1/")) {
    const upstreamPath = pathname.replace(/^\/v1/, "") || "/";
    const upstream = buildBackendUrl(upstreamPath) + req.nextUrl.search;
    return NextResponse.rewrite(new URL(upstream));
  }

  const session = req.cookies.get(SESSION_COOKIE_NAME)?.value;
  const hasSessionCookie = isLoggedInCookie(session);
  const sessionValid = hasSessionCookie && session ? await isSessionValid(session) : false;

  if (pathname === "/login" || pathname === "/register") {
    if (sessionValid) {
      const url = req.nextUrl.clone();
      url.pathname = sanitizeNextPath(searchParams.get("next"));
      url.search = "";
      return NextResponse.redirect(url);
    }
    if (hasSessionCookie) {
      return clearSessionCookie(NextResponse.next());
    }
    return NextResponse.next();
  }

  if (isPublicPath(pathname)) return NextResponse.next();
  if (!isProtectedPath(pathname)) return NextResponse.next();

  if (sessionValid) return NextResponse.next();

  if (pathname.startsWith("/api/")) {
    return clearSessionCookie(
      NextResponse.json({ message: "Unauthorized" }, { status: 401 })
    );
  }

  const loginUrl = req.nextUrl.clone();
  loginUrl.pathname = "/login";
  if (!searchParams.get("next")) {
    loginUrl.searchParams.set("next", `${pathname}${req.nextUrl.search}`);
  }
  return clearSessionCookie(NextResponse.redirect(loginUrl));
}

export const config = {
  matcher: ["/((?!_next/static|_next/image).*)"]
};
