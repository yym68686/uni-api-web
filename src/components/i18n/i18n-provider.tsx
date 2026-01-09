"use client";

import * as React from "react";

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

export function I18nProvider({ locale, children }: I18nProviderProps) {
  const value = React.useMemo<I18nContextValue>(() => {
    return {
      locale,
      t: (key, vars) => translate(locale, key, vars)
    };
  }, [locale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const ctx = React.useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used within <I18nProvider />");
  return ctx;
}

