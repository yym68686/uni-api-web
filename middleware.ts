import { NextResponse, type NextRequest } from "next/server";

const LOCALE_COOKIE_NAME = "uai_locale";
const SUPPORTED_DOCS_LOCALES = new Set(["zh-CN", "en"]);

function isPrefetchRequest(request: NextRequest) {
  const headers = request.headers;
  if (headers.get("x-middleware-prefetch")) return true;

  const purpose = headers.get("purpose") ?? headers.get("sec-purpose");
  if (purpose?.toLowerCase() === "prefetch") return true;

  if (headers.get("next-router-prefetch")) return true;

  return false;
}

export function middleware(request: NextRequest) {
  if (isPrefetchRequest(request)) return NextResponse.next();

  const pathname = request.nextUrl.pathname;
  const segments = pathname.split("/").filter(Boolean);
  const locale = segments[1];

  if (!locale || !SUPPORTED_DOCS_LOCALES.has(locale)) return NextResponse.next();

  const current = request.cookies.get(LOCALE_COOKIE_NAME)?.value;
  if (current === locale) return NextResponse.next();

  const response = NextResponse.next();
  response.cookies.set(LOCALE_COOKIE_NAME, locale, {
    path: "/",
    sameSite: "lax",
    maxAge: 60 * 60 * 24 * 365
  });
  return response;
}

export const config = {
  matcher: ["/docs/:path*"]
};
