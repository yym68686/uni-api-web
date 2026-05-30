"use client";

import * as React from "react";
import { usePathname } from "next/navigation";

import type { Locale, MessageKey, MessageVars } from "@/lib/i18n/messages";
import { t as translate } from "@/lib/i18n/messages";

interface I18nContextValue {
  locale: Locale;
  t: (key: MessageKey, vars?: MessageVars) => string;
}

const I18nContext = React.createContext<I18nContextValue | null>(null);

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

export function I18nProvider({ locale, children }: I18nProviderProps) {
  const pathname = usePathname();
  const explicitPathLocale = localeFromPathname(pathname);
  const previousLocaleProp = React.useRef(locale);
  const [activeLocale, setActiveLocale] = React.useState<Locale>(() => {
    if (typeof window === "undefined") return locale;
    return localeFromPathname(window.location.pathname) ?? locale;
  });

  React.useEffect(() => {
    const localePropChanged = previousLocaleProp.current !== locale;
    previousLocaleProp.current = locale;

    if (explicitPathLocale) {
      setActiveLocale(explicitPathLocale);
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
