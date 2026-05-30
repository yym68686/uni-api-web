import { NextResponse, type NextRequest } from "next/server";

const LOCALE_COOKIE_NAME = "uai_locale";
const SUPPORTED_DOCS_LOCALES = new Set(["zh-CN", "en"]);

type Locale = "zh-CN" | "en";

function isPrefetchRequest(request: NextRequest) {
  const headers = request.headers;
  if (headers.get("x-middleware-prefetch")) return true;

  const purpose = headers.get("purpose") ?? headers.get("sec-purpose");
  if (purpose?.toLowerCase() === "prefetch") return true;

  if (headers.get("next-router-prefetch")) return true;

  return false;
}

function normalizeLocale(value: string | null | undefined): Locale | null {
  if (!value) return null;
  const v = value.trim().toLowerCase();
  if (v === "en" || v.startsWith("en-")) return "en";
  if (v === "zh" || v.startsWith("zh-")) return "zh-CN";
  return null;
}

function detectLocaleFromAcceptLanguage(acceptLanguage: string | null | undefined): Locale {
  const raw = (acceptLanguage ?? "").trim();
  if (!raw) return "en";

  for (const part of raw.split(",")) {
    const tag = part.split(";")[0]?.trim();
    const locale = normalizeLocale(tag);
    if (locale) return locale;
  }

  return "en";
}

function preferredLocale(request: NextRequest): Locale {
  return (
    normalizeLocale(request.cookies.get(LOCALE_COOKIE_NAME)?.value) ??
    detectLocaleFromAcceptLanguage(request.headers.get("accept-language"))
  );
}

function responseWithLocaleCookie(
  request: NextRequest,
  response: NextResponse,
  locale: Locale
) {
  if (request.cookies.get(LOCALE_COOKIE_NAME)?.value === locale) return response;

  response.cookies.set(LOCALE_COOKIE_NAME, locale, {
    path: "/",
    sameSite: "lax",
    maxAge: 60 * 60 * 24 * 365
  });
  return response;
}

export function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname;

  if (pathname === "/") {
    const locale = preferredLocale(request);
    const url = request.nextUrl.clone();
    url.pathname = locale === "zh-CN" ? "/zh-CN" : "/en";
    return NextResponse.redirect(url);
  }

  if (isPrefetchRequest(request)) return NextResponse.next();

  const segments = pathname.split("/").filter(Boolean);
  const docsLocale = normalizeLocale(segments[1]);

  if (!docsLocale || !SUPPORTED_DOCS_LOCALES.has(docsLocale)) return NextResponse.next();

  return responseWithLocaleCookie(request, NextResponse.next(), docsLocale);
}

export const config = {
  matcher: ["/", "/docs/:path*"]
};
