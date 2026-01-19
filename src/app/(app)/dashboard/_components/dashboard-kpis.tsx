import type { Locale } from "@/lib/i18n/messages";
import { getDashboardApiKeys, getDashboardUsage } from "./dashboard-data";
import { DashboardKpisClient } from "./dashboard-kpis-client";

interface DashboardKpisProps {
  locale: Locale;
  remainingCredits: number | null;
}

export async function DashboardKpis({ locale, remainingCredits }: DashboardKpisProps) {
  const [usage, keys] = await Promise.all([getDashboardUsage(), getDashboardApiKeys()]);

  return (
    <DashboardKpisClient
      locale={locale}
      remainingCredits={remainingCredits}
      initialUsage={usage}
      initialKeys={keys}
    />
  );
}
