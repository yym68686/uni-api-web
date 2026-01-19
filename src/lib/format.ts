const COMPACT_NUMBER_FORMATTER = new Intl.NumberFormat("en", {
  notation: "compact",
  maximumFractionDigits: 1
});

const PERCENT_FORMATTER = new Intl.NumberFormat("en", {
  style: "percent",
  maximumFractionDigits: 2
});

const USD_FORMATTER = new Intl.NumberFormat("en", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 4
});

const usdFixed2ByLocale = new Map<string, Intl.NumberFormat>();

export function formatCompactNumber(value: number) {
  return COMPACT_NUMBER_FORMATTER.format(value);
}

export function formatPercent(value: number) {
  return PERCENT_FORMATTER.format(value);
}

export function formatUsd(value: number) {
  return USD_FORMATTER.format(value);
}

export function formatUsdFixed2(value: number, locale: string) {
  const existing = usdFixed2ByLocale.get(locale);
  if (existing) return existing.format(value);

  const created = new Intl.NumberFormat(locale, {
    style: "currency",
    currency: "USD",
    currencyDisplay: "narrowSymbol",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  });
  usdFixed2ByLocale.set(locale, created);
  return created.format(value);
}
