"use client";

import * as React from "react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { UsageAreaChartLazy } from "@/components/charts/usage-area-chart-lazy";
import { usageApiPath } from "@/lib/api-paths";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { getBrowserTimeZone } from "@/lib/timezone";
import type { UsageResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useSwrLite } from "@/lib/swr-lite";

const USAGE_RANGE_OPTIONS = [
  {
    value: "today",
    labelKey: "dashboard.chart.range.today",
    descKey: "dashboard.chart.desc.today"
  },
  {
    value: "7d",
    labelKey: "dashboard.chart.range.7d",
    descKey: "dashboard.chart.desc.7d"
  },
  {
    value: "month",
    labelKey: "dashboard.chart.range.month",
    descKey: "dashboard.chart.desc.month"
  },
  {
    value: "year",
    labelKey: "dashboard.chart.range.year",
    descKey: "dashboard.chart.desc.year"
  }
] as const;

type UsageRange = (typeof USAGE_RANGE_OPTIONS)[number]["value"];

const DEFAULT_USAGE_RANGE: UsageRange = "7d";
const DEFAULT_USAGE_RANGE_OPTION = USAGE_RANGE_OPTIONS[1];
const DAY_MS = 24 * 60 * 60 * 1000;

interface ZonedDateParts {
  year: number;
  month: number;
  day: number;
}

function isUsageResponse(value: unknown): value is UsageResponse {
  if (!value || typeof value !== "object") return false;
  if (!("summary" in value) || !("daily" in value) || !("topModels" in value)) return false;
  const daily = (value as { daily?: unknown }).daily;
  return Array.isArray(daily);
}

async function fetchUsage(url: string) {
  const res = await fetch(url, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isUsageResponse(json)) throw new Error("Invalid response");
  return json;
}

function getRangeOption(range: UsageRange) {
  return USAGE_RANGE_OPTIONS.find((option) => option.value === range) ?? DEFAULT_USAGE_RANGE_OPTION;
}

function getZonedDateParts(date: Date, timeZone: string): ZonedDateParts {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    year: "numeric",
    month: "numeric",
    day: "numeric"
  }).formatToParts(date);
  const values = new Map<string, string>();

  for (const part of parts) {
    if (part.type === "year" || part.type === "month" || part.type === "day") {
      values.set(part.type, part.value);
    }
  }

  const year = Number(values.get("year"));
  const month = Number(values.get("month"));
  const day = Number(values.get("day"));
  if (!Number.isFinite(year) || !Number.isFinite(month) || !Number.isFinite(day)) {
    throw new Error("Invalid zoned date");
  }

  return { year, month, day };
}

function getYearToDateDays(timeZone: string) {
  try {
    const now = getZonedDateParts(new Date(), timeZone);
    const todayUtc = Date.UTC(now.year, now.month - 1, now.day);
    const startUtc = Date.UTC(now.year, 0, 1);
    return Math.floor((todayUtc - startUtc) / DAY_MS) + 1;
  } catch {
    const now = new Date();
    const todayUtc = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
    const startUtc = Date.UTC(now.getFullYear(), 0, 1);
    return Math.floor((todayUtc - startUtc) / DAY_MS) + 1;
  }
}

function getUsageRangeDays(range: UsageRange, timeZone: string) {
  if (range === "today") return 1;
  if (range === "month") return 30;
  if (range === "year") return getYearToDateDays(timeZone);
  return 7;
}

interface DashboardChartClientProps {
  locale: Locale;
  initialTimeZone: string;
  initialUsage: UsageResponse;
}

export function DashboardChartClient({ locale, initialTimeZone, initialUsage }: DashboardChartClientProps) {
  const [hydrated, setHydrated] = React.useState(false);
  const [timeZone, setTimeZone] = React.useState(initialTimeZone);
  const [selectedRange, setSelectedRange] = React.useState<UsageRange>(DEFAULT_USAGE_RANGE);
  const selectedRangeDays = React.useMemo(
    () => getUsageRangeDays(selectedRange, timeZone),
    [selectedRange, timeZone]
  );
  const usagePath = React.useMemo(() => usageApiPath(timeZone, selectedRangeDays), [selectedRangeDays, timeZone]);
  const usageSwr = useSwrLite<UsageResponse>(usagePath, fetchUsage, {
    fallbackData: selectedRange === DEFAULT_USAGE_RANGE ? initialUsage : undefined,
    revalidateOnFocus: true
  });
  const { data: usageData, isValidating: isUsageValidating, mutate: mutateUsage } = usageSwr;

  React.useEffect(() => {
    setHydrated(true);
    const browserTimeZone = getBrowserTimeZone();
    setTimeZone((current) => (current === browserTimeZone ? current : browserTimeZone));
  }, []);

  React.useEffect(() => {
    if (!hydrated) return;
    void mutateUsage(undefined, { revalidate: true });
  }, [hydrated, usagePath, mutateUsage]);

  const activeRangeOption = getRangeOption(selectedRange);
  const usage = hydrated ? usageData : initialUsage;
  const chartData = usage?.daily ?? [];

  return (
    <Card className="lg:col-span-2">
      <CardHeader className="gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1.5">
          <CardTitle>{t(locale, "dashboard.chart.title")}</CardTitle>
          <CardDescription>{t(locale, activeRangeOption.descKey)}</CardDescription>
        </div>
        <div
          role="group"
          aria-label={t(locale, "dashboard.chart.range.label")}
          className="flex w-full flex-wrap gap-1 rounded-xl border border-border bg-background/60 p-1 sm:w-auto"
        >
          {USAGE_RANGE_OPTIONS.map((option) => {
            const active = selectedRange === option.value;

            return (
              <button
                key={option.value}
                type="button"
                aria-pressed={active}
                onClick={() => setSelectedRange(option.value)}
                className={cn(
                  "h-8 min-w-14 flex-1 rounded-lg px-3 text-xs font-medium sm:flex-none",
                  "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  active
                    ? "bg-primary text-primary-foreground shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_8px_22px_oklch(var(--primary)/0.18),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                    : "text-muted-foreground hover:-translate-y-0.5 hover:bg-muted hover:text-foreground"
                )}
              >
                {t(locale, option.labelKey)}
              </button>
            );
          })}
        </div>
      </CardHeader>
      <CardContent aria-busy={isUsageValidating}>
        {chartData.length > 0 ? (
          <UsageAreaChartLazy data={chartData} />
        ) : (
          <div className="h-72 w-full animate-pulse rounded-xl border border-border bg-muted/10" />
        )}
      </CardContent>
    </Card>
  );
}
