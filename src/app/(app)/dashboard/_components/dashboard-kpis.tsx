import { Activity, CreditCard, KeyRound, Wallet } from "lucide-react";

import { StatsCard } from "@/components/app/stats-card";
import { formatCompactNumber, formatUsd } from "@/lib/format";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { getDashboardApiKeys, getDashboardUsage } from "./dashboard-data";

interface DashboardKpisProps {
  locale: Locale;
  remainingCredits: number | null;
}

function buildDaily7d(usageDaily: Array<{ requests: number }>) {
  const last7Days = usageDaily.length > 0 ? usageDaily.slice(-7) : [];
  const requests7d = last7Days.reduce((acc, p) => acc + p.requests, 0);
  return { requests7d };
}

export async function DashboardKpis({ locale, remainingCredits }: DashboardKpisProps) {
  const [usage, keys] = await Promise.all([getDashboardUsage(), getDashboardApiKeys()]);
  const activeKeys = keys.filter((k) => !k.revokedAt).length;

  const { requests7d } = buildDaily7d(usage.daily);
  const spendMonthUsd = Number((usage.summary.spend24hUsd * 30).toFixed(2));

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <StatsCard
        title={t(locale, "dashboard.kpi.totalCalls7d")}
        value={formatCompactNumber(requests7d)}
        trend="+6.1% wow"
        icon={Activity}
        iconGradientClassName="from-primary/40 to-primary/10 text-primary"
      />
      <StatsCard
        title={t(locale, "dashboard.kpi.remainingCredits")}
        value={remainingCredits === null ? "—" : formatUsd(remainingCredits)}
        trend={remainingCredits === null ? "connect billing" : "plan: Pro"}
        icon={Wallet}
        iconGradientClassName="from-success/35 to-success/10 text-success"
      />
      <StatsCard
        title={t(locale, "dashboard.kpi.activeKeys")}
        value={formatCompactNumber(activeKeys)}
        trend="rotate monthly"
        icon={KeyRound}
        iconGradientClassName="from-warning/35 to-warning/10 text-warning"
      />
      <StatsCard
        title={t(locale, "dashboard.kpi.spendMonth")}
        value={formatUsd(spendMonthUsd)}
        trend={`last 24h: ${formatUsd(usage.summary.spend24hUsd)}`}
        icon={CreditCard}
        iconGradientClassName="from-primary/25 to-muted/10 text-foreground"
      />
    </div>
  );
}

