import "server-only";

import { cookies, headers } from "next/headers";

import {
  detectLocaleFromAcceptLanguage,
  LOCALE_COOKIE_NAME,
  type Locale,
  normalizeLocale
} from "@/lib/i18n/messages";

type CookieStore = Awaited<ReturnType<typeof cookies>>;

export async function getRequestLocaleFromCookies(store: CookieStore): Promise<Locale> {
  const fromCookie = normalizeLocale(store.get(LOCALE_COOKIE_NAME)?.value);
  if (fromCookie) return fromCookie;

  const h = await headers();
  return detectLocaleFromAcceptLanguage(h.get("accept-language"));
}

export async function getRequestLocale(): Promise<Locale> {
  return getRequestLocaleFromCookies(await cookies());
}
