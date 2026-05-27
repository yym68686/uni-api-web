"use client";

import { formatDisplayCurrency } from "@/lib/currency";
import type { Locale } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { useDisplayCurrency } from "./currency-provider";

export interface MoneyTextProps {
  valueUsd: number | null | undefined;
  locale: Locale;
  className?: string;
  fallback?: string;
  minimumFractionDigits?: number;
  maximumFractionDigits?: number;
}

export function MoneyText({
  valueUsd,
  locale,
  className,
  fallback = "—",
  minimumFractionDigits,
  maximumFractionDigits
}: MoneyTextProps) {
  const displayCurrency = useDisplayCurrency();

  if (typeof valueUsd !== "number" || !Number.isFinite(valueUsd)) {
    return <span className={className}>{fallback}</span>;
  }

  return (
    <span className={cn("tabular-nums", className)}>
      {formatDisplayCurrency(valueUsd, {
        locale,
        minimumFractionDigits,
        maximumFractionDigits,
        ...displayCurrency
      })}
    </span>
  );
}
