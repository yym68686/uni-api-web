import { Activity, CreditCard, KeyRound, Megaphone, Wallet } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StatsCard } from "@/components/app/stats-card";
import { UsageAreaChartLazy } from "@/components/charts/usage-area-chart-lazy";
import { formatCompactNumber, formatUsd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import type {
  AnnouncementsListResponse,
  ApiKeysListResponse,
  DailyUsagePoint,
  UsageResponse
} from "@/lib/types";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { getCurrentUser } from "@/lib/current-user";

export const dynamic = "force-dynamic";

function isApiKeysListResponse(value: unknown): value is ApiKeysListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isUsageResponse(value: unknown): value is UsageResponse {
  if (!value || typeof value !== "object") return false;
  if (!("summary" in value) || !("daily" in value) || !("topModels" in value)) return false;
  const daily = (value as { daily?: unknown }).daily;
  return Array.isArray(daily);
}

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isoDate(d: Date) {
  return d.toISOString().slice(0, 10);
}

function formatUtcDateTime(value: string) {
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return "—";
  return dt.toISOString().replace("T", " ").slice(0, 16);
}

function buildEmptyDaily(days: number): DailyUsagePoint[] {
  const now = new Date();
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const points: DailyUsagePoint[] = [];
  for (let i = days - 1; i >= 0; i -= 1) {
    const day = new Date(today);
    day.setUTCDate(today.getUTCDate() - i);
    points.push({
      date: isoDate(day),
      requests: 0,
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
      errorRate: 0
    });
  }
  return points;
}

export default async function DashboardPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const userName = me?.email && me.email.length > 0 ? me.email : "User";
  const remainingCredits = typeof me?.balance === "number" && Number.isFinite(me.balance) ? me.balance : null;

  const headers = await getBackendAuthHeadersCached();

  const [usageRes, keysRes, annRes] = await Promise.all([
    fetch(buildBackendUrl("/usage"), { cache: "no-store", headers }).catch(() => null),
    fetch(buildBackendUrl("/keys"), { cache: "no-store", headers }).catch(() => null),
    fetch(buildBackendUrl("/announcements"), { cache: "no-store", headers }).catch(() => null)
  ]);

  let usage: UsageResponse = {
    summary: { requests24h: 0, tokens24h: 0, errorRate24h: 0, spend24hUsd: 0 },
    daily: buildEmptyDaily(7),
    topModels: []
  };
  if (usageRes?.ok) {
    const json: unknown = await usageRes.json().catch(() => null);
    if (isUsageResponse(json)) usage = json;
  }

  let activeKeys = 0;
  if (keysRes?.ok) {
    const json: unknown = await keysRes.json().catch(() => null);
    if (isApiKeysListResponse(json)) activeKeys = json.items.filter((k) => !k.revokedAt).length;
  }
  const last7Days = usage.daily.length > 0 ? usage.daily.slice(-7) : buildEmptyDaily(7);
  const requests7d = last7Days.reduce((acc, p) => acc + p.requests, 0);
  const spendMonthUsd = Number((usage.summary.spend24hUsd * 30).toFixed(2));

  let announcements: AnnouncementsListResponse["items"] = [];
  if (annRes?.ok) {
    const json: unknown = await annRes.json().catch(() => null);
    if (isAnnouncementsListResponse(json)) announcements = json.items;
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          {t(locale, "dashboard.welcomeBack", { name: userName })}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "dashboard.subtitle")}
        </p>
      </div>

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

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>{t(locale, "dashboard.chart.title")}</CardTitle>
            <CardDescription>{t(locale, "dashboard.chart.desc")}</CardDescription>
          </CardHeader>
          <CardContent>
            <UsageAreaChartLazy data={last7Days} />
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card className="bg-warning/10">
            <CardHeader>
              <CardTitle>{t(locale, "dashboard.ann.title")}</CardTitle>
              <CardDescription>{t(locale, "dashboard.ann.desc")}</CardDescription>
            </CardHeader>
            <CardContent>
              {announcements.length === 0 ? (
                <div className="rounded-xl border border-dashed border-border bg-background/20 p-6 text-center text-sm text-muted-foreground">
                  <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl border border-border bg-background/40">
                    <Megaphone className="h-6 w-6 text-muted-foreground uai-float-sm" />
                  </div>
                  <div className="mt-3">
                    {t(locale, "dashboard.ann.empty")}
                  </div>
                </div>
              ) : (
                <div className="space-y-3">
                  {announcements.map((a) => (
                    <div
                      key={a.id}
                      className={cn(
                        "rounded-xl border border-border bg-background/35 p-3",
                        "transition-all duration-300 hover:shadow-lg hover:scale-[1.01]"
                      )}
                    >
                      <div className="text-sm font-medium text-foreground">
                        {a.title}
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground font-mono">
                        {formatUtcDateTime(a.createdAt)} · {a.meta}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
