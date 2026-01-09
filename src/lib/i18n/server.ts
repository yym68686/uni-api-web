import "server-only";

import { cookies, headers } from "next/headers";

import {
  detectLocaleFromAcceptLanguage,
  LOCALE_COOKIE_NAME,
  type Locale,
  normalizeLocale
} from "@/lib/i18n/messages";

export async function getRequestLocale(): Promise<Locale> {
  const store = await cookies();
  const fromCookie = normalizeLocale(store.get(LOCALE_COOKIE_NAME)?.value);
  if (fromCookie) return fromCookie;

  const h = await headers();
  return detectLocaleFromAcceptLanguage(h.get("accept-language"));
}

