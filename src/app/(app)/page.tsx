import { Activity, CreditCard, KeyRound, Wallet } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StatsCard } from "@/components/app/stats-card";
import { UsageAreaChart } from "@/components/charts/usage-area-chart";
import { formatCompactNumber, formatUsd } from "@/lib/format";
import { cn } from "@/lib/utils";
import type {
  AnnouncementsListResponse,
  ApiKeysListResponse,
  DailyUsagePoint,
  UsageResponse
} from "@/lib/types";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";

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
  let userName = "User";
  let remainingCredits: number | null = null;
  try {
    const res = await fetch(buildBackendUrl("/auth/me"), {
      cache: "no-store",
      headers: await getBackendAuthHeaders()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (json && typeof json === "object") {
        const email = (json as { email?: unknown }).email;
        if (typeof email === "string" && email.length > 0) userName = email;
        const balance = (json as { balance?: unknown }).balance;
        if (typeof balance === "number" && Number.isFinite(balance)) remainingCredits = balance;
      }
    }
  } catch {
    // ignore
  }

  let usage: UsageResponse = {
    summary: { requests24h: 0, tokens24h: 0, errorRate24h: 0, spend24hUsd: 0 },
    daily: buildEmptyDaily(7),
    topModels: []
  };
  try {
    const res = await fetch(buildBackendUrl("/usage"), {
      cache: "no-store",
      headers: await getBackendAuthHeaders()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (isUsageResponse(json)) usage = json;
    }
  } catch {
    // Backend not available; keep dashboard resilient with zeros.
  }

  let activeKeys = 0;
  try {
    const keysRes = await fetch(buildBackendUrl("/keys"), {
      cache: "no-store",
      headers: await getBackendAuthHeaders()
    });
    if (keysRes.ok) {
      const json: unknown = await keysRes.json();
      if (isApiKeysListResponse(json)) {
        activeKeys = json.items.filter((k) => !k.revokedAt).length;
      }
    }
  } catch {
    // Backend not available; keep KPI resilient.
  }
  const last7Days = usage.daily.length > 0 ? usage.daily.slice(-7) : buildEmptyDaily(7);
  const requests7d = last7Days.reduce((acc, p) => acc + p.requests, 0);
  const spendMonthUsd = Number((usage.summary.spend24hUsd * 30).toFixed(2));

  let announcements: AnnouncementsListResponse["items"] = [];
  try {
    const res = await fetch(buildBackendUrl("/announcements"), {
      cache: "no-store",
      headers: await getBackendAuthHeaders()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (isAnnouncementsListResponse(json)) announcements = json.items;
    }
  } catch {
    // Backend not available.
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          Welcome back, <span className="text-foreground">{userName}</span>
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          这是你的控制台概览：KPI、近 7 天趋势与公告。
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard
          title="Total calls (7d)"
          value={formatCompactNumber(requests7d)}
          trend="+6.1% wow"
          icon={Activity}
          iconGradientClassName="from-primary/40 to-primary/10 text-primary"
        />
        <StatsCard
          title="Remaining credits"
          value={remainingCredits === null ? "—" : formatCompactNumber(remainingCredits)}
          trend={remainingCredits === null ? "connect billing" : "plan: Pro"}
          icon={Wallet}
          iconGradientClassName="from-success/35 to-success/10 text-success"
        />
        <StatsCard
          title="Active keys"
          value={formatCompactNumber(activeKeys)}
          trend="rotate monthly"
          icon={KeyRound}
          iconGradientClassName="from-warning/35 to-warning/10 text-warning"
        />
        <StatsCard
          title="Spend (month)"
          value={formatUsd(spendMonthUsd)}
          trend={`last 24h: ${formatUsd(usage.summary.spend24hUsd)}`}
          icon={CreditCard}
          iconGradientClassName="from-primary/25 to-muted/10 text-foreground"
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Usage Trend</CardTitle>
            <CardDescription>近 7 天 API 调用趋势</CardDescription>
          </CardHeader>
          <CardContent>
            <UsageAreaChart data={last7Days} />
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card className="bg-warning/10">
            <CardHeader>
              <CardTitle>Announcements</CardTitle>
              <CardDescription>重要变更与近期动态</CardDescription>
            </CardHeader>
            <CardContent>
              {announcements.length === 0 ? (
                <div className="rounded-xl border border-dashed border-border bg-background/20 p-6 text-center text-sm text-muted-foreground">
                  暂无公告
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
                        {a.meta}
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
