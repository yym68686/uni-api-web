import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";

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
  if (pathname.startsWith("/settings")) return true;
  if (pathname.startsWith("/api/keys")) return true;
  if (pathname.startsWith("/api/usage")) return true;
  return false;
}

export function middleware(req: NextRequest) {
  const { pathname, searchParams } = req.nextUrl;

  const session = req.cookies.get(SESSION_COOKIE_NAME)?.value;
  const loggedIn = isLoggedInCookie(session);

  if ((pathname === "/login" || pathname === "/register") && loggedIn) {
    const url = req.nextUrl.clone();
    url.pathname = "/";
    url.search = "";
    return NextResponse.redirect(url);
  }

  if (isPublicPath(pathname)) return NextResponse.next();
  if (!isProtectedPath(pathname)) return NextResponse.next();

  if (loggedIn) return NextResponse.next();

  if (pathname.startsWith("/api/")) {
    return NextResponse.json({ message: "Unauthorized" }, { status: 401 });
  }

  const loginUrl = req.nextUrl.clone();
  loginUrl.pathname = "/login";
  if (!searchParams.get("next")) {
    loginUrl.searchParams.set("next", pathname);
  }
  return NextResponse.redirect(loginUrl);
}

export const config = {
  matcher: ["/((?!_next/static|_next/image).*)"]
};
