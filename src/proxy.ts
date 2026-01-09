import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";
import {
  detectLocaleFromAcceptLanguage,
  LOCALE_COOKIE_NAME,
  normalizeLocale
} from "@/lib/i18n/messages";

function normalizeBaseUrl(raw: string) {
  return raw.trim().replace(/\/+$/, "");
}

function buildBackendUrl(path: string) {
  const base = normalizeBaseUrl(process.env.API_BASE_URL ?? "http://localhost:8001/v1");
  const normalizedPath = path.replace(/^\/+/, "");
  return new URL(normalizedPath, `${base}/`).toString();
}

function sanitizeNextPath(value: string | null, fallback: string) {
  if (!value) return fallback;
  if (!value.startsWith("/")) return fallback;
  if (value.startsWith("//")) return fallback;
  return value;
}

function isSecureRequest(req: NextRequest) {
  return (
    req.headers.get("x-forwarded-proto") === "https" ||
    req.nextUrl.protocol === "https:"
  );
}

function clearSessionCookie(req: NextRequest, res: NextResponse) {
  res.cookies.set(SESSION_COOKIE_NAME, "", {
    httpOnly: true,
    sameSite: "lax",
    secure: isSecureRequest(req),
    path: "/",
    maxAge: 0
  });
  return res;
}

function ensureLocaleCookie(req: NextRequest, res: NextResponse) {
  const existing = normalizeLocale(req.cookies.get(LOCALE_COOKIE_NAME)?.value);
  if (existing) return res;

  const locale = detectLocaleFromAcceptLanguage(req.headers.get("accept-language"));
  res.cookies.set(LOCALE_COOKIE_NAME, locale, {
    httpOnly: false,
    sameSite: "lax",
    secure: isSecureRequest(req),
    path: "/",
    maxAge: 60 * 60 * 24 * 365
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
  if (pathname === "/dashboard" || pathname.startsWith("/dashboard/")) return true;
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
  const shouldValidateSession =
    pathname === "/login" ||
    pathname === "/register" ||
    isProtectedPath(pathname);
  const sessionValid =
    shouldValidateSession && hasSessionCookie && session
      ? await isSessionValid(session)
      : false;

  if (pathname === "/login" || pathname === "/register") {
    if (sessionValid) {
      const url = req.nextUrl.clone();
      url.pathname = sanitizeNextPath(searchParams.get("next"), "/dashboard");
      url.search = "";
      return ensureLocaleCookie(req, NextResponse.redirect(url));
    }
    if (hasSessionCookie) {
      return ensureLocaleCookie(req, clearSessionCookie(req, NextResponse.next()));
    }
    return ensureLocaleCookie(req, NextResponse.next());
  }

  if (isPublicPath(pathname)) return ensureLocaleCookie(req, NextResponse.next());
  if (!isProtectedPath(pathname)) return ensureLocaleCookie(req, NextResponse.next());

  if (sessionValid) return ensureLocaleCookie(req, NextResponse.next());

  if (pathname.startsWith("/api/")) {
    return clearSessionCookie(
      req,
      ensureLocaleCookie(req, NextResponse.json({ message: "Unauthorized" }, { status: 401 }))
    );
  }

  const loginUrl = req.nextUrl.clone();
  loginUrl.pathname = "/login";
  if (!searchParams.get("next")) {
    loginUrl.searchParams.set("next", `${pathname}${req.nextUrl.search}`);
  }
  return ensureLocaleCookie(req, clearSessionCookie(req, NextResponse.redirect(loginUrl)));
}

export const config = {
  matcher: ["/((?!_next/static|_next/image).*)"]
};
