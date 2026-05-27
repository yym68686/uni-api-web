"use client";

import * as React from "react";
import { Check, CircleDollarSign, DollarSign, type LucideIcon } from "lucide-react";
import { toast } from "sonner";

import { useDisplayCurrency } from "@/components/currency/currency-provider";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  displayCurrencyCode,
  formatDisplayCurrencyFixed2,
  type DisplayCurrency
} from "@/lib/currency";
import type { MessageKey } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";

const currencyOptions = [
  {
    value: "USD",
    titleKey: "settings.currency.usd.title",
    descKey: "settings.currency.usd.desc",
    icon: DollarSign
  },
  {
    value: "CNY",
    titleKey: "settings.currency.cny.title",
    descKey: "settings.currency.cny.desc",
    icon: CircleDollarSign
  }
] as const satisfies ReadonlyArray<{
  value: DisplayCurrency;
  titleKey: MessageKey;
  descKey: MessageKey;
  icon: LucideIcon;
}>;

export function SettingsPanel() {
  const { t, locale } = useI18n();
  const { currency, cnyPerUsd, setCurrency } = useDisplayCurrency();
  const rateText = React.useMemo(() => {
    return new Intl.NumberFormat(locale, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 4
    }).format(cnyPerUsd);
  }, [cnyPerUsd, locale]);
  const preview = formatDisplayCurrencyFixed2(12.34, { locale, currency, cnyPerUsd });

  function chooseCurrency(nextCurrency: DisplayCurrency) {
    if (nextCurrency === currency) return;
    setCurrency(nextCurrency);
    toast.success(t("settings.currency.toast"));
  }

  return (
    <Card className="overflow-hidden">
      <CardHeader className="gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1">
          <CardTitle>{t("settings.display.title")}</CardTitle>
          <CardDescription>{t("settings.display.desc")}</CardDescription>
        </div>
        <Badge variant="secondary" className="w-fit rounded-full px-3 py-1 font-mono">
          {displayCurrencyCode(currency)}
        </Badge>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-2">
          <div>
            <div className="text-sm font-medium text-foreground">{t("settings.currency.title")}</div>
            <div className="mt-1 text-sm text-muted-foreground">{t("settings.currency.desc")}</div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            {currencyOptions.map((option) => {
              const active = currency === option.value;
              const Icon = option.icon;

              return (
                <button
                  key={option.value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => chooseCurrency(option.value)}
                  className={cn(
                    "group flex min-h-28 items-start gap-4 rounded-xl border p-4 text-left",
                    "bg-background/35 transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    "motion-reduce:transform-none motion-reduce:transition-none",
                    active
                      ? "border-primary/45 bg-primary/10 shadow-[0_0_0_1px_oklch(var(--primary)/0.2),0_12px_30px_oklch(var(--primary)/0.12),inset_0_1px_0_0_oklch(var(--foreground)/0.08)]"
                      : "border-border hover:-translate-y-1 hover:border-primary/20 hover:bg-background/55"
                  )}
                >
                  <span
                    className={cn(
                      "flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border",
                      "transition-colors duration-300",
                      active
                        ? "border-primary/35 bg-primary/20 text-primary"
                        : "border-border bg-muted/25 text-muted-foreground group-hover:text-foreground"
                    )}
                  >
                    <Icon className="h-5 w-5" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center justify-between gap-3">
                      <span className="font-medium text-foreground">{t(option.titleKey)}</span>
                      {active ? <Check className="h-4 w-4 shrink-0 text-primary" /> : null}
                    </span>
                    <span className="mt-1 block text-sm text-muted-foreground">{t(option.descKey)}</span>
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        <div className="grid gap-3 rounded-xl border border-border bg-muted/10 p-4 sm:grid-cols-[1fr_auto] sm:items-center">
          <div className="space-y-1">
            <div className="text-sm font-medium text-foreground">{t("settings.currency.preview")}</div>
            <div className="text-xs text-muted-foreground">
              {t("settings.currency.rate", { amount: rateText })}
            </div>
            <div className="text-xs text-muted-foreground">{t("settings.currency.note")}</div>
          </div>
          <div className="font-mono text-2xl font-semibold tabular-nums text-foreground sm:text-right">
            {preview}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
