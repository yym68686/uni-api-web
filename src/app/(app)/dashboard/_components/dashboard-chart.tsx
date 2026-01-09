import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { UsageAreaChartLazy } from "@/components/charts/usage-area-chart-lazy";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { getDashboardUsage } from "./dashboard-data";

interface DashboardChartProps {
  locale: Locale;
}

export async function DashboardChart({ locale }: DashboardChartProps) {
  const usage = await getDashboardUsage();
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

