import type { Locale } from "@/lib/i18n/messages";
import { getDashboardUsage } from "./dashboard-data";
import { DashboardChartClient } from "./dashboard-chart-client";

interface DashboardChartProps {
  locale: Locale;
}

export async function DashboardChart({ locale }: DashboardChartProps) {
  const usage = await getDashboardUsage();

  return <DashboardChartClient locale={locale} initialUsage={usage} />;
}
