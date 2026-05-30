"use client";

import * as React from "react";
import { usePathname } from "next/navigation";

import type { Locale, MessageKey, MessageVars } from "@/lib/i18n/messages";
import { LOCALE_COOKIE_NAME, normalizeLocale, t as translate } from "@/lib/i18n/messages";

interface I18nContextValue {
  locale: Locale;
  t: (key: MessageKey, vars?: MessageVars) => string;
}

const I18nContext = React.createContext<I18nContextValue | null>(null);
const CLIENT_LOCALE_HINT_KEY = "uai_locale_hint";

interface I18nProviderProps {
  locale: Locale;
  children: React.ReactNode;
}

function localeFromPathname(pathname: string | null | undefined): Locale | null {
  if (!pathname) return null;
  if (pathname === "/zh-CN" || pathname.startsWith("/zh-CN/")) return "zh-CN";
  if (pathname === "/en" || pathname.startsWith("/en/")) return "en";
  if (pathname === "/docs/zh-CN" || pathname.startsWith("/docs/zh-CN/")) return "zh-CN";
  if (pathname === "/docs/en" || pathname.startsWith("/docs/en/")) return "en";
  return null;
}

function readCookieLocale(): Locale | null {
  if (typeof document === "undefined") return null;
  const prefix = `${encodeURIComponent(LOCALE_COOKIE_NAME)}=`;
  for (const part of document.cookie.split(";")) {
    const value = part.trim();
    if (!value.startsWith(prefix)) continue;
    try {
      return normalizeLocale(decodeURIComponent(value.slice(prefix.length)));
    } catch {
      return normalizeLocale(value.slice(prefix.length));
    }
  }
  return null;
}

function readStoredLocaleHint(): Locale | null {
  if (typeof window === "undefined") return null;
  try {
    return normalizeLocale(window.sessionStorage.getItem(CLIENT_LOCALE_HINT_KEY));
  } catch {
    return null;
  }
}

export function rememberClientLocaleHint(locale: Locale | null) {
  if (typeof window === "undefined") return;
  try {
    if (locale) {
      window.sessionStorage.setItem(CLIENT_LOCALE_HINT_KEY, locale);
    } else {
      window.sessionStorage.removeItem(CLIENT_LOCALE_HINT_KEY);
    }
  } catch {}
}

function initialClientLocale(fallback: Locale): Locale {
  if (typeof window === "undefined") return fallback;
  return (
    localeFromPathname(window.location.pathname) ??
    readCookieLocale() ??
    readStoredLocaleHint() ??
    fallback
  );
}

export function I18nProvider({ locale, children }: I18nProviderProps) {
  const pathname = usePathname();
  const explicitPathLocale = localeFromPathname(pathname);
  const previousLocaleProp = React.useRef(locale);
  const [activeLocale, setActiveLocale] = React.useState<Locale>(() => initialClientLocale(locale));

  React.useEffect(() => {
    const localePropChanged = previousLocaleProp.current !== locale;
    previousLocaleProp.current = locale;

    if (explicitPathLocale) {
      rememberClientLocaleHint(explicitPathLocale);
      setActiveLocale(explicitPathLocale);
      return;
    }

    const cookieLocale = readCookieLocale();
    if (cookieLocale) {
      rememberClientLocaleHint(cookieLocale);
      setActiveLocale(cookieLocale);
      return;
    }

    if (localePropChanged) {
      setActiveLocale(locale);
    }
  }, [explicitPathLocale, locale]);

  React.useEffect(() => {
    document.documentElement.lang = activeLocale;
  }, [activeLocale]);

  const value = React.useMemo<I18nContextValue>(() => {
    return {
      locale: activeLocale,
      t: (key, vars) => translate(activeLocale, key, vars)
    };
  }, [activeLocale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const ctx = React.useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used within <I18nProvider />");
  return ctx;
}
