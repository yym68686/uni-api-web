"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Activity, AlertTriangle, BarChart3, Clock, RefreshCw, Users } from "lucide-react";

import type { AdminAnalyticsPreset, AdminAnalyticsResponse, AdminAnalyticsTab } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import { EmptyState } from "@/components/common/empty-state";
import { StatsCard } from "@/components/app/stats-card";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

function isTab(value: string | null): value is AdminAnalyticsTab {
  return value === "summary" || value === "users" || value === "models" || value === "errors" || value === "performance";
}

function isPreset(value: string | null): value is AdminAnalyticsPreset {
  return value === "today" || value === "week" || value === "month" || value === "year" || value === "all" || value === "custom";
}

function percent(value: number) {
  return `${(value * 100).toFixed(2)}%`;
}

function compactNumber(locale: string) {
  return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 2 });
}

function formatMs(ms: number | null | undefined) {
  if (ms == null) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

function toYmdInput(value: string) {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) ? value : "";
}

function chartTick(ts: string, granularity: AdminAnalyticsResponse["range"]["granularity"], locale: string) {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  const fmt = new Intl.DateTimeFormat(locale, granularity === "hour"
    ? { month: "2-digit", day: "2-digit", hour: "2-digit" }
    : { month: "2-digit", day: "2-digit" }
  );
  return fmt.format(d);
}

interface AdminAnalyticsViewClientProps {
  initialTab: AdminAnalyticsTab;
  initialRange: AdminAnalyticsResponse["range"];
  initialData: AdminAnalyticsResponse | null;
}

export function AdminAnalyticsViewClient({ initialTab, initialRange, initialData }: AdminAnalyticsViewClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { t, locale } = useI18n();

  const tab = React.useMemo(() => {
    const raw = searchParams.get("tab");
    return isTab(raw) ? raw : initialTab;
  }, [initialTab, searchParams]);

  const preset = React.useMemo(() => {
    const raw = searchParams.get("range");
    return isPreset(raw) ? raw : initialRange.preset;
  }, [initialRange.preset, searchParams]);

  const data = initialData;
  const numberCompact = React.useMemo(() => compactNumber(locale), [locale]);
  const currency = React.useMemo(() => new Intl.NumberFormat(locale, {
    style: "currency",
    currency: "USD",
    currencyDisplay: "narrowSymbol",
    maximumFractionDigits: 2
  }), [locale]);

  const errorRate = React.useMemo(() => {
    const calls = data?.kpis.calls ?? 0;
    const errors = data?.kpis.errors ?? 0;
    return calls > 0 ? errors / calls : 0;
  }, [data]);

  function pushParams(next: URLSearchParams) {
    const qs = next.toString();
    router.push(qs.length > 0 ? `?${qs}` : "?", { scroll: false });
  }

  function setTab(nextTab: AdminAnalyticsTab) {
    const next = new URLSearchParams(searchParams.toString());
    next.set("tab", nextTab);
    pushParams(next);
  }

  function setPreset(nextPreset: AdminAnalyticsPreset) {
    const next = new URLSearchParams(searchParams.toString());
    next.set("range", nextPreset);
    next.delete("from");
    next.delete("to");
    if (!next.get("tab")) next.set("tab", tab);
    pushParams(next);
  }

  const [customOpen, setCustomOpen] = React.useState(false);
  const [customFrom, setCustomFrom] = React.useState<string>(toYmdInput(initialRange.from));
  const [customTo, setCustomTo] = React.useState<string>(toYmdInput(initialRange.to));

  React.useEffect(() => {
    setCustomFrom(toYmdInput(initialRange.from));
    setCustomTo(toYmdInput(initialRange.to));
  }, [initialRange.from, initialRange.to]);

  function applyCustom() {
    if (!customFrom || !customTo) return;
    const next = new URLSearchParams(searchParams.toString());
    next.set("range", "custom");
    next.set("from", customFrom);
    next.set("to", customTo);
    if (!next.get("tab")) next.set("tab", tab);
    setCustomOpen(false);
    pushParams(next);
  }

  const series = React.useMemo(() => {
    const points = data?.series ?? [];
    const maxPoints = 480;
    const step = points.length > maxPoints ? Math.ceil(points.length / maxPoints) : 1;
    const sampled = step === 1
      ? points
      : points.filter((_, idx) => idx % step === 0 || idx === points.length - 1);

    return sampled.map((p) => ({
      ...p,
      tokensTotal: p.inputTokens + p.outputTokens + p.cachedTokens
    }));
  }, [data]);

  const rangeLabel = React.useMemo(() => {
    switch (preset) {
      case "today":
        return t("admin.analytics.range.today");
      case "month":
        return t("admin.analytics.range.month");
      case "year":
        return t("admin.analytics.range.year");
      case "all":
        return t("admin.analytics.range.all");
      case "custom":
        return t("admin.analytics.range.custom");
      case "week":
      default:
        return t("admin.analytics.range.week");
    }
  }, [preset, t]);

  const rangeHint = React.useMemo(() => {
    const r = data?.range ?? initialRange;
    return t("admin.analytics.range.hint", { from: r.from, to: r.to });
  }, [data?.range, initialRange, t]);

  return (
    <div className="space-y-6">
      <div className="sticky top-3 z-20">
        <Card className={cn(
          "rounded-2xl border border-border bg-background/70 backdrop-blur-xl",
          "shadow-[0_0_0_1px_oklch(var(--border)/0.6),0_14px_40px_oklch(0%_0_0/0.35)]"
        )}>
          <CardContent className="p-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex flex-wrap items-center gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant={preset === "today" ? "secondary" : "outline"}
                    size="sm"
                    className="rounded-xl bg-transparent"
                    onClick={() => setPreset("today")}
                  >
                    {t("admin.analytics.range.today")}
                  </Button>
                  <Button
                    type="button"
                    variant={preset === "week" ? "secondary" : "outline"}
                    size="sm"
                    className="rounded-xl bg-transparent"
                    onClick={() => setPreset("week")}
                  >
                    {t("admin.analytics.range.week")}
                  </Button>
                  <Button
                    type="button"
                    variant={preset === "month" ? "secondary" : "outline"}
                    size="sm"
                    className="rounded-xl bg-transparent"
                    onClick={() => setPreset("month")}
                  >
                    {t("admin.analytics.range.month")}
                  </Button>
                  <Button
                    type="button"
                    variant={preset === "year" ? "secondary" : "outline"}
                    size="sm"
                    className="rounded-xl bg-transparent"
                    onClick={() => setPreset("year")}
                  >
                    {t("admin.analytics.range.year")}
                  </Button>
                  <Button
                    type="button"
                    variant={preset === "all" ? "secondary" : "outline"}
                    size="sm"
                    className="rounded-xl bg-transparent"
                    onClick={() => setPreset("all")}
                  >
                    {t("admin.analytics.range.all")}
                  </Button>

                  <Dialog open={customOpen} onOpenChange={setCustomOpen}>
                    <DialogTrigger asChild>
                      <Button
                        type="button"
                        variant={preset === "custom" ? "secondary" : "outline"}
                        size="sm"
                        className="rounded-xl bg-transparent"
                      >
                        {t("admin.analytics.range.custom")}
                      </Button>
                    </DialogTrigger>
                    <DialogContent className="max-w-lg">
                      <DialogHeader>
                        <DialogTitle>{t("admin.analytics.range.custom")}</DialogTitle>
                        <DialogDescription>
                          {t("admin.analytics.custom.from")} / {t("admin.analytics.custom.to")}
                        </DialogDescription>
                      </DialogHeader>

                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <div className="space-y-2">
                          <Label htmlFor="uai-analytics-from">{t("admin.analytics.custom.from")}</Label>
                          <Input
                            id="uai-analytics-from"
                            type="date"
                            value={customFrom}
                            onChange={(e) => setCustomFrom(e.target.value)}
                          />
                        </div>
                        <div className="space-y-2">
                          <Label htmlFor="uai-analytics-to">{t("admin.analytics.custom.to")}</Label>
                          <Input
                            id="uai-analytics-to"
                            type="date"
                            value={customTo}
                            onChange={(e) => setCustomTo(e.target.value)}
                          />
                        </div>
                      </div>

                      <DialogFooter>
                        <Button type="button" variant="outline" className="rounded-xl bg-transparent" onClick={() => setCustomOpen(false)}>
                          {t("admin.analytics.custom.cancel")}
                        </Button>
                        <Button type="button" onClick={() => applyCustom()}>
                          {t("admin.analytics.custom.apply")}
                        </Button>
                      </DialogFooter>
                    </DialogContent>
                  </Dialog>

                  <span className="ml-1 inline-flex items-center gap-2 rounded-xl border border-border bg-muted/10 px-3 py-2">
                    <BarChart3 className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm font-medium text-foreground">{rangeLabel}</span>
                    <span className="text-xs text-muted-foreground font-mono">{rangeHint}</span>
                  </span>
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  className="rounded-xl bg-transparent"
                  onClick={() => router.refresh()}
                >
                  <RefreshCw className="h-4 w-4" />
                  {t("common.refresh")}
                </Button>
              </div>
            </div>

            <div className="mt-3 flex flex-wrap items-center gap-2">
              {([
                { id: "summary", label: t("admin.analytics.tab.summary") },
                { id: "users", label: t("admin.analytics.tab.users") },
                { id: "models", label: t("admin.analytics.tab.models") },
                { id: "errors", label: t("admin.analytics.tab.errors") },
                { id: "performance", label: t("admin.analytics.tab.performance") }
              ] as const).map((item) => (
                <Button
                  key={item.id}
                  type="button"
                  variant={tab === item.id ? "secondary" : "outline"}
                  size="sm"
                  className="rounded-xl bg-transparent"
                  onClick={() => setTab(item.id)}
                >
                  {item.label}
                </Button>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      {!data ? (
        <EmptyState
          icon={<AlertTriangle className="h-5 w-5 text-muted-foreground" />}
          title={t("admin.analytics.empty.title")}
          description={t("admin.analytics.empty.desc")}
        />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
            <StatsCard
              title={t("admin.analytics.kpi.spend")}
              value={currency.format(data.kpis.spendUsd)}
              icon={BarChart3}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.calls")}
              value={numberCompact.format(data.kpis.calls)}
              icon={Activity}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.totalTokens")}
              value={numberCompact.format(data.kpis.inputTokens + data.kpis.outputTokens + data.kpis.cachedTokens)}
              icon={Activity}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.errorRate")}
              value={percent(errorRate)}
              icon={AlertTriangle}
              trend={rangeLabel}
            />
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
            <StatsCard
              title={t("admin.analytics.kpi.activeUsers")}
              value={numberCompact.format(data.kpis.activeUsers)}
              icon={Users}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.p95Latency")}
              value={formatMs(data.kpis.p95LatencyMs)}
              icon={Clock}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.p95Ttft")}
              value={formatMs(data.kpis.p95TtftMs)}
              icon={Clock}
              trend={rangeLabel}
            />
            <StatsCard
              title={t("admin.analytics.kpi.cachedTokens")}
              value={numberCompact.format(data.kpis.cachedTokens)}
              icon={Activity}
              trend="cached"
            />
          </div>
        </>
      )}

      {data && tab === "summary" ? (
        <div className="space-y-6">
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <Card className="rounded-2xl">
              <CardHeader>
                <CardTitle>{t("admin.analytics.chart.spendCalls")}</CardTitle>
                <CardDescription className="font-mono">{rangeHint}</CardDescription>
              </CardHeader>
              <CardContent>
                <div className="h-64">
                  <ResponsiveContainer width="100%" height="100%">
                    <LineChart data={series}>
                      <CartesianGrid stroke="oklch(var(--border))" strokeOpacity={0.6} vertical={false} />
                      <XAxis
                        dataKey="ts"
                        tickMargin={8}
                        minTickGap={18}
                        tickFormatter={(v) => chartTick(String(v), data?.range.granularity ?? initialRange.granularity, locale)}
                        stroke="oklch(var(--muted-foreground))"
                      />
                      <YAxis
                        yAxisId="left"
                        tickMargin={10}
                        tickFormatter={(v) => String(v)}
                        stroke="oklch(var(--muted-foreground))"
                      />
                      <YAxis
                        yAxisId="right"
                        orientation="right"
                        tickMargin={10}
                        tickFormatter={(v) => String(v)}
                        stroke="oklch(var(--muted-foreground))"
                      />
                      <Tooltip
                        contentStyle={{
                          backgroundColor: "oklch(var(--popover) / 0.9)",
                          border: "1px solid oklch(var(--border))",
                          borderRadius: 12
                        }}
                        labelStyle={{ color: "oklch(var(--foreground))" }}
                      />
                      <Line
                        yAxisId="left"
                        type="monotone"
                        dataKey="spendUsd"
                        stroke="oklch(var(--primary))"
                        strokeWidth={2}
                        dot={false}
                        name={t("admin.analytics.kpi.spend")}
                      />
                      <Line
                        yAxisId="right"
                        type="monotone"
                        dataKey="calls"
                        stroke="oklch(var(--foreground))"
                        strokeOpacity={0.55}
                        strokeWidth={2}
                        dot={false}
                        name={t("admin.analytics.kpi.calls")}
                      />
                    </LineChart>
                  </ResponsiveContainer>
                </div>
              </CardContent>
            </Card>

            <Card className="rounded-2xl">
              <CardHeader>
                <CardTitle>{t("admin.analytics.chart.tokens")}</CardTitle>
                <CardDescription className="font-mono">{rangeHint}</CardDescription>
              </CardHeader>
              <CardContent>
                <div className="h-64">
                  <ResponsiveContainer width="100%" height="100%">
                    <AreaChart data={series}>
                      <CartesianGrid stroke="oklch(var(--border))" strokeOpacity={0.6} vertical={false} />
                      <XAxis
                        dataKey="ts"
                        tickMargin={8}
                        minTickGap={18}
                        tickFormatter={(v) => chartTick(String(v), data?.range.granularity ?? initialRange.granularity, locale)}
                        stroke="oklch(var(--muted-foreground))"
                      />
                      <YAxis tickMargin={10} stroke="oklch(var(--muted-foreground))" />
                      <Tooltip
                        contentStyle={{
                          backgroundColor: "oklch(var(--popover) / 0.9)",
                          border: "1px solid oklch(var(--border))",
                          borderRadius: 12
                        }}
                        labelStyle={{ color: "oklch(var(--foreground))" }}
                      />
                      <Area
                        type="monotone"
                        dataKey="inputTokens"
                        stackId="1"
                        stroke="oklch(var(--primary))"
                        fill="oklch(var(--primary) / 0.25)"
                        name="input"
                      />
                      <Area
                        type="monotone"
                        dataKey="outputTokens"
                        stackId="1"
                        stroke="oklch(var(--foreground) / 0.75)"
                        fill="oklch(var(--foreground) / 0.14)"
                        name="output"
                      />
                      <Area
                        type="monotone"
                        dataKey="cachedTokens"
                        stackId="1"
                        stroke="oklch(var(--muted-foreground) / 0.9)"
                        fill="oklch(var(--muted-foreground) / 0.16)"
                        name="cached"
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                </div>
              </CardContent>
            </Card>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
            <Card className="rounded-2xl">
              <CardHeader>
                <CardTitle>{t("admin.analytics.leaders.users")}</CardTitle>
                <CardDescription>{t("admin.analytics.kpi.spend")}</CardDescription>
              </CardHeader>
              <CardContent className="p-0">
                <Table variant="card">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-12">#</TableHead>
                      <TableHead>{t("admin.users.table.email")}</TableHead>
                      <TableHead className="text-right">{t("admin.analytics.kpi.spend")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(data?.leaders.users ?? []).slice(0, 8).map((u, idx) => (
                      <TableRow key={u.userId}>
                        <TableCell className="font-mono text-xs text-muted-foreground">{idx + 1}</TableCell>
                        <TableCell className="min-w-0">
                          <div className="truncate text-sm font-medium text-foreground">{u.email ?? u.userId}</div>
                          <div className="mt-0.5 text-xs text-muted-foreground font-mono tabular-nums">
                            {numberCompact.format(u.totalTokens)} tokens · {numberCompact.format(u.calls)} calls
                          </div>
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-sm">{currency.format(u.spendUsd)}</TableCell>
                      </TableRow>
                    ))}
                    {(data?.leaders.users ?? []).length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={3} className="py-6 text-center text-sm text-muted-foreground">
                          —
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            <Card className="rounded-2xl">
              <CardHeader>
                <CardTitle>{t("admin.analytics.leaders.models")}</CardTitle>
                <CardDescription>{t("admin.analytics.kpi.tokens")}</CardDescription>
              </CardHeader>
              <CardContent className="p-0">
                <Table variant="card">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-12">#</TableHead>
                      <TableHead>{t("app.models")}</TableHead>
                      <TableHead className="text-right">{t("admin.analytics.kpi.tokens")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(data?.leaders.models ?? []).slice(0, 8).map((m, idx) => (
                      <TableRow key={m.model}>
                        <TableCell className="font-mono text-xs text-muted-foreground">{idx + 1}</TableCell>
                        <TableCell className="min-w-0">
                          <div className="truncate text-sm font-medium text-foreground">{m.model}</div>
                          <div className="mt-0.5 text-xs text-muted-foreground font-mono tabular-nums">
                            {numberCompact.format(m.calls)} calls · {numberCompact.format(m.errors)} errors
                          </div>
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-sm">{numberCompact.format(m.totalTokens)}</TableCell>
                      </TableRow>
                    ))}
                    {(data?.leaders.models ?? []).length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={3} className="py-6 text-center text-sm text-muted-foreground">
                          —
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            <Card className="rounded-2xl">
              <CardHeader>
                <CardTitle>{t("admin.analytics.leaders.errors")}</CardTitle>
                <CardDescription>{t("admin.analytics.tab.errors")}</CardDescription>
              </CardHeader>
              <CardContent className="p-0">
                <Table variant="card">
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("admin.analytics.table.error")}</TableHead>
                      <TableHead className="text-right">{t("admin.analytics.table.count")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(data?.leaders.errors ?? []).slice(0, 8).map((e) => (
                      <TableRow key={e.key}>
                        <TableCell className="font-mono text-sm">{e.key}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-sm">
                          {numberCompact.format(e.count)}
                          {e.share != null ? (
                            <span className="ml-2 text-xs text-muted-foreground">{percent(e.share)}</span>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    ))}
                    {(data?.leaders.errors ?? []).length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={2} className="py-6 text-center text-sm text-muted-foreground">
                          —
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          </div>
        </div>
      ) : null}

      {data && tab === "users" ? (
        <Card className="rounded-2xl">
          <CardHeader>
            <CardTitle>{t("admin.analytics.tab.users")}</CardTitle>
            <CardDescription className="font-mono">{rangeHint}</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <Table variant="card">
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead>
                  <TableHead>{t("admin.users.table.email")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.spend")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.calls")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.tab.errors")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.tokens")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data?.leaders.users ?? []).map((u, idx) => (
                  <TableRow key={u.userId} className="uai-cv-auto">
                    <TableCell className="font-mono text-xs text-muted-foreground">{idx + 1}</TableCell>
                    <TableCell className="min-w-0">
                      <div className="truncate font-medium text-foreground">{u.email ?? u.userId}</div>
                      <div className="mt-0.5 text-xs text-muted-foreground font-mono">{u.userId}</div>
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{currency.format(u.spendUsd)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(u.calls)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(u.errors)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(u.totalTokens)}</TableCell>
                  </TableRow>
                ))}
                {(data?.leaders.users ?? []).length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="py-10">
                      <EmptyState
                        icon={<Users className="h-5 w-5 text-muted-foreground" />}
                        title={t("admin.analytics.empty.title")}
                        description={t("admin.analytics.empty.desc")}
                      />
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ) : null}

      {data && tab === "models" ? (
        <Card className="rounded-2xl">
          <CardHeader>
            <CardTitle>{t("admin.analytics.tab.models")}</CardTitle>
            <CardDescription className="font-mono">{rangeHint}</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <Table variant="card">
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead>
                  <TableHead>{t("app.models")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.spend")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.calls")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.tab.errors")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.kpi.tokens")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data?.leaders.models ?? []).map((m, idx) => (
                  <TableRow key={m.model} className="uai-cv-auto">
                    <TableCell className="font-mono text-xs text-muted-foreground">{idx + 1}</TableCell>
                    <TableCell className="font-mono text-sm">{m.model}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{currency.format(m.spendUsd)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(m.calls)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(m.errors)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{numberCompact.format(m.totalTokens)}</TableCell>
                  </TableRow>
                ))}
                {(data?.leaders.models ?? []).length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="py-10">
                      <EmptyState
                        icon={<BarChart3 className="h-5 w-5 text-muted-foreground" />}
                        title={t("admin.analytics.empty.title")}
                        description={t("admin.analytics.empty.desc")}
                      />
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ) : null}

      {data && tab === "errors" ? (
        <Card className="rounded-2xl">
          <CardHeader>
            <CardTitle>{t("admin.analytics.tab.errors")}</CardTitle>
            <CardDescription className="font-mono">{rangeHint}</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <Table variant="card">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("admin.analytics.table.error")}</TableHead>
                  <TableHead className="text-right">{t("admin.analytics.table.count")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data?.leaders.errors ?? []).map((e) => (
                  <TableRow key={e.key} className="uai-cv-auto">
                    <TableCell className="font-mono text-sm">{e.key}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {numberCompact.format(e.count)}
                      {e.share != null ? (
                        <span className="ml-2 text-xs text-muted-foreground">{percent(e.share)}</span>
                      ) : null}
                    </TableCell>
                  </TableRow>
                ))}
                {(data?.leaders.errors ?? []).length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={2} className="py-10">
                      <EmptyState
                        icon={<AlertTriangle className="h-5 w-5 text-muted-foreground" />}
                        title={t("admin.analytics.empty.title")}
                        description={t("admin.analytics.empty.desc")}
                      />
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ) : null}

      {data && tab === "performance" ? (
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Card className="rounded-2xl">
            <CardHeader>
              <CardTitle>{t("admin.analytics.kpi.p95Latency")}</CardTitle>
              <CardDescription className="font-mono">{rangeHint}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-semibold tracking-tight tabular-nums">{formatMs(data?.kpis.p95LatencyMs ?? null)}</div>
              <div className="mt-1 text-sm text-muted-foreground font-mono">p95 totalDurationMs</div>
            </CardContent>
          </Card>
          <Card className="rounded-2xl">
            <CardHeader>
              <CardTitle>{t("admin.analytics.kpi.p95Ttft")}</CardTitle>
              <CardDescription className="font-mono">{rangeHint}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="text-3xl font-semibold tracking-tight tabular-nums">{formatMs(data?.kpis.p95TtftMs ?? null)}</div>
              <div className="mt-1 text-sm text-muted-foreground font-mono">p95 ttftMs</div>
            </CardContent>
          </Card>
        </div>
      ) : null}
    </div>
  );
}
