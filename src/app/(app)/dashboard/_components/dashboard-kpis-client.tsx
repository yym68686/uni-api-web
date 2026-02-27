"use client";

import * as React from "react";
import { Activity, CreditCard, KeyRound, Wallet } from "lucide-react";

import { StatsCard } from "@/components/app/stats-card";
import { API_PATHS } from "@/lib/api-paths";
import { formatCompactNumber, formatUsd } from "@/lib/format";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { ApiKeysListResponse, UsageResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";

interface AuthMeResponse {
  balance: number;
}

function isApiKeysListResponse(value: unknown): value is ApiKeysListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isAuthMeResponse(value: unknown): value is AuthMeResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as { balance?: unknown };
  return typeof v.balance === "number" && Number.isFinite(v.balance);
}

function isUsageResponse(value: unknown): value is UsageResponse {
  if (!value || typeof value !== "object") return false;
  if (!("summary" in value) || !("daily" in value) || !("topModels" in value)) return false;
  const daily = (value as { daily?: unknown }).daily;
  return Array.isArray(daily);
}

async function fetchJson(url: string) {
  const res = await fetch(url, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const message =
      json && typeof json === "object" && "message" in json
        ? String((json as { message?: unknown }).message ?? "Request failed")
        : "Request failed";
    throw new Error(message);
  }
  return json;
}

async function fetchUsage() {
  const json = await fetchJson(API_PATHS.usage);
  if (!isUsageResponse(json)) throw new Error("Invalid response");
  return json;
}

async function fetchKeys() {
  const json = await fetchJson(API_PATHS.keys);
  if (!isApiKeysListResponse(json)) throw new Error("Invalid response");
  return json.items;
}

async function fetchRemainingCredits() {
  const json = await fetchJson(API_PATHS.authMe);
  if (!isAuthMeResponse(json)) throw new Error("Invalid response");
  return json.balance;
}

interface DashboardKpisClientProps {
  locale: Locale;
  remainingCredits: number | null;
  initialUsage: UsageResponse;
  initialKeys: ApiKeysListResponse["items"];
}

function buildDaily7d(usageDaily: Array<{ requests: number }>) {
  const last7Days = usageDaily.length > 0 ? usageDaily.slice(-7) : [];
  const requests7d = last7Days.reduce((acc, p) => acc + p.requests, 0);
  return { requests7d };
}

export function DashboardKpisClient({
  locale,
  remainingCredits,
  initialUsage,
  initialKeys
}: DashboardKpisClientProps) {
  const [hydrated, setHydrated] = React.useState(false);

  const usageSwr = useSwrLite<UsageResponse>(API_PATHS.usage, fetchUsage, {
    fallbackData: initialUsage,
    revalidateOnFocus: true
  });

  const keysSwr = useSwrLite<ApiKeysListResponse["items"]>(API_PATHS.keys, fetchKeys, {
    fallbackData: initialKeys,
    revalidateOnFocus: true
  });

  const remainingCreditsSwr = useSwrLite<number>(API_PATHS.authMe, fetchRemainingCredits, {
    fallbackData: remainingCredits ?? undefined,
    revalidateOnFocus: true
  });

  React.useEffect(() => {
    setHydrated(true);
  }, []);

  React.useEffect(() => {
    if (!hydrated) return;
    const interval = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      void remainingCreditsSwr.mutate(fetchRemainingCredits(), { revalidate: false });
    }, 15_000);
    return () => window.clearInterval(interval);
  }, [hydrated, remainingCreditsSwr.mutate]);

  const usage = hydrated ? (usageSwr.data ?? initialUsage) : initialUsage;
  const keys = hydrated ? (keysSwr.data ?? initialKeys) : initialKeys;
  const liveRemainingCredits = hydrated ? (remainingCreditsSwr.data ?? remainingCredits) : remainingCredits;
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
        value={liveRemainingCredits === null ? "—" : formatUsd(liveRemainingCredits)}
        trend={liveRemainingCredits === null ? "connect billing" : "plan: Pro"}
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
