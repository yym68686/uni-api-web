"use client";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { UsageAreaChartLazy } from "@/components/charts/usage-area-chart-lazy";
import { API_PATHS } from "@/lib/api-paths";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { UsageResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";

function isUsageResponse(value: unknown): value is UsageResponse {
  if (!value || typeof value !== "object") return false;
  if (!("summary" in value) || !("daily" in value) || !("topModels" in value)) return false;
  const daily = (value as { daily?: unknown }).daily;
  return Array.isArray(daily);
}

async function fetchUsage() {
  const res = await fetch(API_PATHS.usage, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isUsageResponse(json)) throw new Error("Invalid response");
  return json;
}

interface DashboardChartClientProps {
  locale: Locale;
  initialUsage: UsageResponse;
}

export function DashboardChartClient({ locale, initialUsage }: DashboardChartClientProps) {
  const { data } = useSwrLite<UsageResponse>(API_PATHS.usage, fetchUsage, {
    fallbackData: initialUsage,
    revalidateOnFocus: true
  });

  const usage = data ?? initialUsage;
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

