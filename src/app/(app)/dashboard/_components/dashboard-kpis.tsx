import type { Locale } from "@/lib/i18n/messages";
import { getDashboardApiKeys, getDashboardUsage } from "./dashboard-data";
import { DashboardKpisClient } from "./dashboard-kpis-client";

interface DashboardKpisProps {
  locale: Locale;
  remainingCredits: number | null;
  initialTimeZone: string;
}

export async function DashboardKpis({ locale, remainingCredits, initialTimeZone }: DashboardKpisProps) {
  const [usage, keys] = await Promise.all([getDashboardUsage(initialTimeZone), getDashboardApiKeys()]);

  return (
    <DashboardKpisClient
      locale={locale}
      remainingCredits={remainingCredits}
      initialTimeZone={initialTimeZone}
      initialUsage={usage}
      initialKeys={keys}
    />
  );
}
