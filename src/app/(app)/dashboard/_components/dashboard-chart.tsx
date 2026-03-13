import type { Locale } from "@/lib/i18n/messages";
import { getDashboardUsage } from "./dashboard-data";
import { DashboardChartClient } from "./dashboard-chart-client";

interface DashboardChartProps {
  locale: Locale;
  initialTimeZone: string;
}

export async function DashboardChart({ locale, initialTimeZone }: DashboardChartProps) {
  const usage = await getDashboardUsage(initialTimeZone);

  return <DashboardChartClient locale={locale} initialTimeZone={initialTimeZone} initialUsage={usage} />;
}
