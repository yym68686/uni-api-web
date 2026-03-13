"use client";

import * as React from "react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { UsageAreaChartLazy } from "@/components/charts/usage-area-chart-lazy";
import { usageApiPath } from "@/lib/api-paths";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { getBrowserTimeZone } from "@/lib/timezone";
import type { UsageResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";

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

interface DashboardChartClientProps {
  locale: Locale;
  initialTimeZone: string;
  initialUsage: UsageResponse;
}

export function DashboardChartClient({ locale, initialTimeZone, initialUsage }: DashboardChartClientProps) {
  const [hydrated, setHydrated] = React.useState(false);
  const [timeZone, setTimeZone] = React.useState(initialTimeZone);
  const usagePath = React.useMemo(() => usageApiPath(timeZone), [timeZone]);
  const usageSwr = useSwrLite<UsageResponse>(usagePath, fetchUsage, {
    fallbackData: initialUsage,
    revalidateOnFocus: true
  });

  React.useEffect(() => {
    setHydrated(true);
    const browserTimeZone = getBrowserTimeZone();
    setTimeZone((current) => (current === browserTimeZone ? current : browserTimeZone));
  }, []);

  React.useEffect(() => {
    if (!hydrated) return;
    void usageSwr.mutate(undefined, { revalidate: true });
  }, [hydrated, usagePath, usageSwr.mutate]);

  const usage = hydrated ? (usageSwr.data ?? initialUsage) : initialUsage;
  const last7Days = usage.daily.length > 0 ? usage.daily.slice(-7) : usage.daily;

  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <CardTitle>{t(locale, "dashboard.chart.title")}</CardTitle>
        <CardDescription>{t(locale, "dashboard.chart.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        <UsageAreaChartLazy data={last7Days} />
      </CardContent>
    </Card>
  );
}
