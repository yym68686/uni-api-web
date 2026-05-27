"use client";

import * as React from "react";

import {
  DEFAULT_DISPLAY_CURRENCY,
  DISPLAY_CURRENCY_STORAGE_KEY,
  isDisplayCurrency,
  parseCnyPerUsd,
  type DisplayCurrency
} from "@/lib/currency";

interface CurrencyContextValue {
  currency: DisplayCurrency;
  cnyPerUsd: number;
  setCurrency: (currency: DisplayCurrency) => void;
}

const CurrencyContext = React.createContext<CurrencyContextValue | null>(null);

export interface CurrencyProviderProps {
  initialCnyPerUsd: number;
  children: React.ReactNode;
}

export function CurrencyProvider({ initialCnyPerUsd, children }: CurrencyProviderProps) {
  const [currency, setCurrencyState] = React.useState<DisplayCurrency>(DEFAULT_DISPLAY_CURRENCY);
  const cnyPerUsd = React.useMemo(() => parseCnyPerUsd(initialCnyPerUsd), [initialCnyPerUsd]);

  React.useEffect(() => {
    try {
      const saved = window.localStorage.getItem(DISPLAY_CURRENCY_STORAGE_KEY);
      if (isDisplayCurrency(saved)) setCurrencyState(saved);
    } catch {
      // Ignore storage errors and keep the default USD display.
    }
  }, []);

  React.useEffect(() => {
    function onStorage(event: StorageEvent) {
      if (event.key !== DISPLAY_CURRENCY_STORAGE_KEY) return;
      if (isDisplayCurrency(event.newValue)) {
        setCurrencyState(event.newValue);
      }
    }

    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const setCurrency = React.useCallback((nextCurrency: DisplayCurrency) => {
    setCurrencyState(nextCurrency);
    try {
      window.localStorage.setItem(DISPLAY_CURRENCY_STORAGE_KEY, nextCurrency);
    } catch {
      // The current tab can still use the selected currency for this session.
    }
  }, []);

  const value = React.useMemo<CurrencyContextValue>(
    () => ({ currency, cnyPerUsd, setCurrency }),
    [currency, cnyPerUsd, setCurrency]
  );

  return <CurrencyContext.Provider value={value}>{children}</CurrencyContext.Provider>;
}

export function useDisplayCurrency() {
  const ctx = React.useContext(CurrencyContext);
  if (!ctx) throw new Error("useDisplayCurrency must be used within <CurrencyProvider />");
  return ctx;
}
