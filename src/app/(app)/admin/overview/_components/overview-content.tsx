import Link from "next/link";
import { AlertTriangle, Ban, CheckCircle2, LayoutDashboard, Megaphone, PlugZap, Settings, Users } from "lucide-react";

import type { Locale } from "@/lib/i18n/messages";
import type { AdminOverviewEventItem, AdminOverviewHealthItem, AdminOverviewResponse } from "@/lib/types";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import { t } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { StatsCard } from "@/components/app/stats-card";
import { AdminModelsRefreshButton } from "@/components/admin/models-refresh-button";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

function isAdminOverviewResponse(value: unknown): value is AdminOverviewResponse {
  if (!value || typeof value !== "object") return false;
  if (!("kpis" in value) || !("counts" in value) || !("health" in value) || !("events" in value)) return false;
  const kpis = (value as { kpis?: unknown }).kpis;
  const counts = (value as { counts?: unknown }).counts;
  return Boolean(kpis && typeof kpis === "object" && counts && typeof counts === "object");
}

async function getOverview() {
  const res = await fetch(buildBackendUrl("/admin/overview"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.adminOverview], revalidate: 30 },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminOverviewResponse(json)) return null;
  return json;
}

function healthBadgeVariant(level: AdminOverviewHealthItem["level"]) {
  switch (level) {
    case "destructive":
      return "destructive";
    case "warning":
      return "warning";
    case "info":
    default:
      return "default";
  }
}

function healthLevelLabel(locale: Locale, level: AdminOverviewHealthItem["level"]) {
  switch (level) {
    case "destructive":
      return t(locale, "admin.overview.health.level.destructive");
    case "warning":
      return t(locale, "admin.overview.health.level.warning");
    case "info":
    default:
      return t(locale, "admin.overview.health.level.info");
  }
}

function healthIcon(id: string) {
  switch (id) {
    case "users_banned":
      return Ban;
    default:
      return AlertTriangle;
  }
}

function healthHref(id: string) {
  switch (id) {
    case "no_channels":
      return "/admin/channels";
    case "registration_disabled":
      return "/admin/settings";
    case "models_disabled":
      return "/admin/models";
    case "users_banned":
      return "/admin/users";
    default:
      return "/admin/overview";
  }
}

function healthLabel(locale: Locale, item: AdminOverviewHealthItem) {
  switch (item.id) {
    case "no_channels":
      return t(locale, "admin.overview.health.noChannels");
    case "registration_disabled":
      return t(locale, "admin.overview.health.registrationDisabled");
    case "models_disabled":
      return t(locale, "admin.overview.health.modelsDisabled", { count: String(item.value ?? 0) });
    case "users_banned":
      return t(locale, "admin.overview.health.usersBanned", { count: String(item.value ?? 0) });
    case "errors_24h":
      return t(locale, "admin.overview.health.errors24h", { count: String(item.value ?? 0) });
    default:
      return item.id;
  }
}

function eventLabel(locale: Locale, event: AdminOverviewEventItem) {
  switch (event.type) {
    case "user_created":
      return t(locale, "admin.overview.event.userCreated", { email: event.email ?? t(locale, "common.unknown") });
    case "balance_adjusted": {
      const delta = typeof event.deltaUsd === "number" ? event.deltaUsd : 0;
      const prefix = delta >= 0 ? "+" : "";
      return t(locale, "admin.overview.event.balanceAdjusted", {
        email: event.email ?? t(locale, "common.unknown"),
        delta: `${prefix}$${Math.abs(delta).toFixed(2)}`
      });
    }
    case "announcement_published": {
      const title = event.titleZh && locale.startsWith("zh") ? event.titleZh : event.titleEn || event.title || "";
      return t(locale, "admin.overview.event.announcementPublished", {
        title: title.length > 0 ? title : t(locale, "common.unknown")
      });
    }
    case "channel_updated":
      return t(locale, "admin.overview.event.channelUpdated", { name: event.channelName ?? t(locale, "common.unknown") });
    default:
      return event.type;
  }
}

function eventIcon(type: AdminOverviewEventItem["type"]) {
  switch (type) {
    case "user_created":
      return Users;
    case "balance_adjusted":
      return LayoutDashboard;
    case "announcement_published":
      return Megaphone;
    case "channel_updated":
      return PlugZap;
    default:
      return LayoutDashboard;
  }
}

export async function AdminOverviewContent({ locale }: { locale: Locale }) {
  const overview = await getOverview();
  if (!overview) return null;

  const number = new Intl.NumberFormat(locale);
  const calls = number.format(overview.kpis.calls24h);
  const spend = `$${overview.kpis.spendUsd24h.toFixed(2)}`;
  const activeUsers = number.format(overview.kpis.activeUsers24h);
  const activeKeys = number.format(overview.kpis.activeKeys24h);

  const hasHealth = overview.health.length > 0;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="flex-row items-start justify-between gap-4">
          <div className="space-y-1">
            <CardTitle>{t(locale, "admin.overview.health.title")}</CardTitle>
            <CardDescription>{t(locale, "admin.overview.health.desc")}</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <AdminModelsRefreshButton />
            <Button asChild variant="outline" className="rounded-xl bg-transparent">
              <Link href="/admin/settings">
                <Settings className="h-4 w-4" />
                {t(locale, "app.admin.settings")}
              </Link>
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {hasHealth ? (
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {overview.health.map((item) => {
                const Icon = healthIcon(item.id);
                return (
                  <Button
                    key={item.id}
                    asChild
                    variant="outline"
                    className={cn(
                      "h-auto justify-start gap-2 rounded-xl bg-transparent px-3 py-2 text-left",
                      "hover:bg-muted/30"
                    )}
                  >
                    <Link href={healthHref(item.id)}>
                      <span className="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-inset ring-border/60">
                        <Icon className="h-4 w-4" />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center justify-between gap-2">
                          <span className="truncate text-sm font-medium text-foreground">{healthLabel(locale, item)}</span>
                          <Badge variant={healthBadgeVariant(item.level)} className="shrink-0">
                            {healthLevelLabel(locale, item.level)}
                          </Badge>
                        </div>
                      </div>
                    </Link>
                  </Button>
                );
              })}
            </div>
          ) : (
            <div className="flex items-center gap-3 rounded-xl border border-border bg-muted/10 p-4">
              <span className="inline-flex h-9 w-9 items-center justify-center rounded-xl bg-success/15 text-success ring-1 ring-inset ring-success/25">
                <CheckCircle2 className="h-4 w-4" />
              </span>
              <div className="text-sm font-medium text-foreground">{t(locale, "admin.overview.health.ok")}</div>
            </div>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <StatsCard title={t(locale, "admin.overview.kpi.calls")} value={calls} icon={LayoutDashboard} />
        <StatsCard title={t(locale, "admin.overview.kpi.spend")} value={spend} icon={LayoutDashboard} />
        <StatsCard title={t(locale, "admin.overview.kpi.activeUsers")} value={activeUsers} icon={Users} />
        <StatsCard title={t(locale, "admin.overview.kpi.activeKeys")} value={activeKeys} icon={PlugZap} />
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>{t(locale, "admin.overview.activity.title")}</CardTitle>
            <CardDescription>{t(locale, "admin.overview.activity.desc")}</CardDescription>
          </CardHeader>
          <CardContent>
            {overview.events.length === 0 ? (
              <div className="text-sm text-muted-foreground">{t(locale, "admin.overview.activity.empty")}</div>
            ) : (
              <ul className="space-y-2">
                {overview.events.map((event) => {
                  const Icon = eventIcon(event.type);
                  return (
                    <li key={event.id}>
                      <Link
                        href={event.href}
                        className={cn(
                          "group flex items-center justify-between gap-4 rounded-xl border border-border bg-muted/10 px-4 py-3",
                          "transition-colors hover:bg-muted/20"
                        )}
                      >
                        <div className="flex min-w-0 items-center gap-3">
                          <span className="inline-flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-inset ring-border/60">
                            <Icon className="h-4 w-4" />
                          </span>
                          <div className="min-w-0">
                            <div className="truncate text-sm font-medium text-foreground">{eventLabel(locale, event)}</div>
                            {event.type === "balance_adjusted" ? (
                              <div className="mt-0.5 text-xs text-muted-foreground font-mono tabular-nums">
                                {typeof event.balanceUsd === "number"
                                  ? `$${event.balanceUsd.toFixed(2)}`
                                  : "—"}
                              </div>
                            ) : null}
                          </div>
                        </div>
                        <ClientDateTime value={event.createdAt} locale={locale} className="shrink-0 text-xs text-muted-foreground" />
                      </Link>
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t(locale, "admin.overview.actions.title")}</CardTitle>
            <CardDescription>{t(locale, "admin.overview.actions.desc")}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            <Button asChild variant="outline" className="w-full justify-start rounded-xl bg-transparent">
              <Link href="/admin/users">
                <Users className="h-4 w-4" />
                {t(locale, "admin.overview.actions.openUsers")}
              </Link>
            </Button>
            <Button asChild variant="outline" className="w-full justify-start rounded-xl bg-transparent">
              <Link href="/admin/channels">
                <PlugZap className="h-4 w-4" />
                {t(locale, "admin.overview.actions.openChannels")}
              </Link>
            </Button>
            <Button asChild variant="outline" className="w-full justify-start rounded-xl bg-transparent">
              <Link href="/admin/models">
                <LayoutDashboard className="h-4 w-4" />
                {t(locale, "admin.overview.actions.openModels")}
              </Link>
            </Button>
            <Button asChild variant="outline" className="w-full justify-start rounded-xl bg-transparent">
              <Link href="/admin/announcements">
                <Megaphone className="h-4 w-4" />
                {t(locale, "admin.overview.actions.openAnnouncements")}
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
