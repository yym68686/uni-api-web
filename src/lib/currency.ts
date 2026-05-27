import type { Locale } from "@/lib/i18n/messages";

export type DisplayCurrency = "USD" | "CNY";

export const DISPLAY_CURRENCY_STORAGE_KEY = "uai-display-currency";
export const DEFAULT_DISPLAY_CURRENCY: DisplayCurrency = "USD";
export const DEFAULT_CNY_PER_USD = 7.2;

interface DisplayCurrencyFormatOptions {
  locale: Locale | string;
  currency: DisplayCurrency;
  cnyPerUsd: number;
  minimumFractionDigits?: number;
  maximumFractionDigits?: number;
}

export function isDisplayCurrency(value: string | null | undefined): value is DisplayCurrency {
  return value === "USD" || value === "CNY";
}

export function parseCnyPerUsd(value: string | number | null | undefined) {
  const parsed = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return DEFAULT_CNY_PER_USD;
  return parsed;
}

export function displayCurrencyCode(currency: DisplayCurrency) {
  return currency === "CNY" ? "CNY" : "USD";
}

export function displayCurrencySymbol(currency: DisplayCurrency) {
  return currency === "CNY" ? "¥" : "$";
}

export function convertUsdToDisplay(valueUsd: number, currency: DisplayCurrency, cnyPerUsd: number) {
  if (!Number.isFinite(valueUsd)) return 0;
  if (currency === "CNY") return valueUsd * cnyPerUsd;
  return valueUsd;
}

export function formatDisplayCurrency(valueUsd: number, options: DisplayCurrencyFormatOptions) {
  const {
    locale,
    currency,
    cnyPerUsd,
    minimumFractionDigits,
    maximumFractionDigits = 4
  } = options;
  const value = convertUsdToDisplay(valueUsd, currency, cnyPerUsd);

  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: displayCurrencyCode(currency),
    currencyDisplay: "narrowSymbol",
    minimumFractionDigits,
    maximumFractionDigits
  }).format(value);
}

export function formatDisplayCurrencyFixed2(
  valueUsd: number,
  options: Pick<DisplayCurrencyFormatOptions, "locale" | "currency" | "cnyPerUsd">
) {
  return formatDisplayCurrency(valueUsd, {
    ...options,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  });
}

export function formatDisplayUsdPrice(value: string | null | undefined, options: DisplayCurrencyFormatOptions) {
  if (!value) return "—";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return `${displayCurrencySymbol(options.currency)}${value}`;

  const displayValue = convertUsdToDisplay(parsed, options.currency, options.cnyPerUsd);
  const maxDigits = Math.abs(displayValue) > 0 && Math.abs(displayValue) < 0.01 ? 6 : 4;
  return formatDisplayCurrency(parsed, {
    ...options,
    maximumFractionDigits: maxDigits
  });
}
