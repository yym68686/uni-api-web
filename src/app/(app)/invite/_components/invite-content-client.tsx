"use client";

import * as React from "react";
import { CheckCircle2, Clock, Copy, Eye, Gift, Users } from "lucide-react";
import { toast } from "sonner";

import { API_PATHS } from "@/lib/api-paths";
import type { InviteSummaryResponse } from "@/lib/types";
import { useSwrLite } from "@/lib/swr-lite";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey } from "@/lib/i18n/messages";
import { StatsCard } from "@/components/app/stats-card";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { InviteContentSkeleton } from "./invite-skeleton";

function isInviteSummaryResponse(value: unknown): value is InviteSummaryResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (typeof v.inviteCode !== "string") return false;
  if (typeof v.invitedTotal !== "number") return false;
  if (typeof v.visitsTotal !== "number") return false;
  if (typeof v.rewardsPending !== "number") return false;
  if (typeof v.rewardsConfirmed !== "number") return false;
  if (!Array.isArray(v.items)) return false;
  return true;
}

async function fetchInviteSummary() {
  const res = await fetch(API_PATHS.inviteSummary, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const message =
      json && typeof json === "object" && "message" in json
        ? String((json as { message?: unknown }).message ?? "Request failed")
        : "Request failed";
    throw new Error(message);
  }
  if (!isInviteSummaryResponse(json)) throw new Error("Invalid response");
  return json;
}

function statusVariant(status: string) {
  if (status === "confirmed") return "success";
  if (status === "pending") return "warning";
  if (status === "blocked" || status === "reversed") return "destructive";
  return "secondary";
}

function statusLabelKey(status: string): MessageKey {
  if (status === "pending") return "invite.status.pending";
  if (status === "confirmed") return "invite.status.confirmed";
  if (status === "blocked") return "invite.status.blocked";
  if (status === "reversed") return "invite.status.reversed";
  return "invite.status.none";
}

interface InviteContentClientProps {
  initialSummary: InviteSummaryResponse | null;
}

export function InviteContentClient({ initialSummary }: InviteContentClientProps) {
  const { t, locale } = useI18n();
  const [origin, setOrigin] = React.useState<string | null>(null);
  const currencyFormatter = React.useMemo(() => {
    return new Intl.NumberFormat(locale, {
      style: "currency",
      currency: "USD",
      currencyDisplay: "narrowSymbol",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    });
  }, [locale]);
  const { data } = useSwrLite<InviteSummaryResponse>(API_PATHS.inviteSummary, fetchInviteSummary, {
    fallbackData: initialSummary ?? undefined,
    dedupingIntervalMs: 0,
    revalidateOnFocus: true
  });

  React.useEffect(() => {
    setOrigin(window.location.origin);
  }, []);

  if (data === undefined && initialSummary === null) return <InviteContentSkeleton />;

  const summary = data ?? initialSummary;
  if (!summary) return <InviteContentSkeleton />;

  const invitePath = `/register?ref=${encodeURIComponent(summary.inviteCode)}`;
  const inviteLink = origin ? `${origin}${invitePath}` : invitePath;

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t("common.copied"));
    } catch {
      toast.error(t("common.copyFailed"));
    }
  }

  const kpiTrend = t("invite.kpi.trend");
  const conversionRate =
    summary.visitsTotal > 0
      ? `${((summary.invitedTotal / summary.visitsTotal) * 100).toFixed(1)}%`
      : "—";
  const conversionLabel = t("invite.kpi.conversion", { rate: conversionRate });

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <Gift className="h-4 w-4 text-muted-foreground" />
              {t("invite.link.title")}
            </CardTitle>
            <div className="text-sm text-muted-foreground">{t("invite.link.desc")}</div>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="min-w-0 space-y-2">
            <div className="flex h-10 min-w-0 items-center justify-between gap-2 rounded-xl border border-border bg-background/40 px-3">
              <div className="min-w-0 font-mono text-xs text-foreground">
                <span className="block truncate">{inviteLink}</span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-8 w-8 rounded-xl"
                onClick={() => void copy(inviteLink)}
                aria-label={t("invite.link.copy")}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard
          title={t("invite.kpi.invited")}
          value={String(summary.invitedTotal)}
          trend={kpiTrend}
          icon={Users}
          iconGradientClassName="from-primary/30 to-primary/10 text-primary"
        />
        <StatsCard
          title={t("invite.kpi.visits")}
          value={String(summary.visitsTotal)}
          trend={conversionLabel}
          icon={Eye}
          iconGradientClassName="from-muted/30 to-muted/10 text-muted-foreground"
        />
        <StatsCard
          title={t("invite.kpi.pending")}
          value={String(summary.rewardsPending)}
          trend={t("invite.kpi.pendingTrend")}
          icon={Clock}
          iconGradientClassName="from-warning/30 to-warning/10 text-warning"
        />
        <StatsCard
          title={t("invite.kpi.confirmed")}
          value={String(summary.rewardsConfirmed)}
          trend={t("invite.kpi.confirmedTrend")}
          icon={CheckCircle2}
          iconGradientClassName="from-success/30 to-success/10 text-success"
        />
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <div className="space-y-1">
            <CardTitle>{t("invite.table.title")}</CardTitle>
            <div className="text-sm text-muted-foreground">{t("invite.table.desc")}</div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {summary.items.length === 0 ? (
            <div className="p-6">
              <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border bg-background/40 px-6 py-10 text-center">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl border border-border bg-muted/40">
                  <Users className="h-5 w-5 text-muted-foreground" />
                </div>
                <div className="mt-4 text-sm font-medium text-foreground">{t("invite.empty.title")}</div>
                <div className="mt-1 text-sm text-muted-foreground">{t("invite.empty.desc")}</div>
              </div>
            </div>
          ) : (
            <Table variant="card">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("invite.table.email")}</TableHead>
                  <TableHead>{t("invite.table.status")}</TableHead>
                  <TableHead className="text-right">{t("invite.table.reward")}</TableHead>
                  <TableHead className="text-right">{t("invite.table.time")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {summary.items.map((row) => (
                  <TableRow key={row.id} className={cn("uai-cv-auto", "hover:bg-muted/50")}>
                    <TableCell className="whitespace-nowrap font-mono text-xs text-foreground">{row.email}</TableCell>
                    <TableCell>
                      <Badge variant={statusVariant(row.rewardStatus)} className="capitalize">
                        {t(statusLabelKey(row.rewardStatus))}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums text-sm">
                      {typeof row.rewardUsd === "number" && Number.isFinite(row.rewardUsd)
                        ? currencyFormatter.format(row.rewardUsd)
                        : "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums text-xs text-muted-foreground">
                      <ClientDateTime value={row.invitedAt} locale={locale} timeStyle="medium" />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
