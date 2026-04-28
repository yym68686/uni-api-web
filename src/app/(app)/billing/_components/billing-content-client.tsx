"use client";

import * as React from "react";
import { z } from "zod";
import { ArrowUpRight, Clock, CreditCard, Gift, Loader2, RefreshCw } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { StatsCard } from "@/components/app/stats-card";
import { ClientDateTime } from "@/components/common/client-datetime";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { BillingTableClient } from "./billing-table-client";
import { API_PATHS, billingLedgerListApiPath, billingTopupStatusApiPath } from "@/lib/api-paths";
import { formatUsdFixed2 } from "@/lib/format";
import { isInviteSummaryResponse } from "@/lib/invite-summary";
import { mutateSwrLite, useSwrLite } from "@/lib/swr-lite";
import type {
  BillingLedgerItem,
  BillingLedgerListResponse,
  BillingPaymentMethod,
  BillingTopupCheckoutResponse,
  BillingTopupStatusResponse,
  InviteSummaryResponse
} from "@/lib/types";
import type { Locale, MessageKey, MessageVars } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { BillingContentSkeleton } from "./billing-skeleton";

interface AuthMeResponse {
  balance: number;
}

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
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

async function fetchLedger(key: string) {
  const res = await fetch(key, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isBillingLedgerListResponse(json)) throw new Error("Invalid response");
  return json.items;
}

async function fetchCurrentBalance(key: string) {
  const res = await fetch(key, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isAuthMeResponse(json)) throw new Error("Invalid response");
  return json.balance;
}

async function fetchInviteSummary(key: string) {
  const res = await fetch(key, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isInviteSummaryResponse(json)) throw new Error("Invalid response");
  return json;
}

function isBillingTopupCheckoutResponse(value: unknown): value is BillingTopupCheckoutResponse {
  if (!value || typeof value !== "object") return false;
  const obj = value as Record<string, unknown>;
  return typeof obj.checkoutUrl === "string" && typeof obj.requestId === "string";
}

function isBillingTopupStatusResponse(value: unknown): value is BillingTopupStatusResponse {
  if (!value || typeof value !== "object") return false;
  const obj = value as Record<string, unknown>;
  if (typeof obj.requestId !== "string") return false;
  if (typeof obj.status !== "string") return false;
  if (typeof obj.units !== "number") return false;
  if (!("newBalance" in obj)) return true;
  return obj.newBalance === null || typeof obj.newBalance === "number";
}

function createTopupSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.object({
    amountUsd: z.coerce
      .number()
      .finite()
      .refine((value) => Number.isInteger(value), t("billing.topup.validation.integer"))
      .min(5, t("billing.topup.validation.min", { min: 5 }))
      .max(5000, t("billing.topup.validation.max", { max: 5000 })),
    paymentMethod: z.enum(["card", "alipay", "wxpay"])
  });
}

type TopupFormValues = z.infer<ReturnType<typeof createTopupSchema>>;

function waitMs(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve) => {
    const id = window.setTimeout(() => resolve(), ms);
    if (!signal) return;
    signal.addEventListener(
      "abort",
      () => {
        window.clearTimeout(id);
        resolve();
      },
      { once: true }
    );
  });
}

function clearTopupQueryParams() {
  const url = new URL(window.location.href);
  [
    "request_id",
    "requestId",
    "out_trade_no",
    "trade_no",
    "api_trade_no",
    "trade_status",
    "type",
    "name",
    "money",
    "buyer",
    "pid",
    "sign",
    "sign_type",
    "timestamp",
    "param"
  ].forEach((key) => url.searchParams.delete(key));
  if (url.toString() === window.location.href) return;
  window.history.replaceState(null, "", url.toString());
}

interface BillingContentClientProps {
  locale: Locale;
  initialItems: BillingLedgerItem[] | null;
  initialBalance: number | null;
  pageSize: number;
  topupEnabled: boolean;
  autoRevalidate?: boolean;
}

export function BillingContentClient({
  locale,
  initialItems,
  initialBalance,
  pageSize,
  topupEnabled,
  autoRevalidate = true
}: BillingContentClientProps) {
  const { t } = useI18n();
  const searchParams = useSearchParams();

  const topupRequestIdFromUrl = React.useMemo(() => {
    const raw =
      searchParams.get("request_id") ??
      searchParams.get("requestId") ??
      searchParams.get("out_trade_no") ??
      searchParams.get("param");
    if (!raw) return null;
    const trimmed = raw.trim();
    return trimmed.length > 0 ? trimmed : null;
  }, [searchParams]);

  const key = billingLedgerListApiPath(pageSize, 0);
  const { data, mutate } = useSwrLite<BillingLedgerItem[]>(key, fetchLedger, {
    fallbackData: initialItems ?? undefined,
    revalidateOnFocus: false
  });
  const { data: currentBalanceData, mutate: mutateCurrentBalance } = useSwrLite<number>(API_PATHS.authMe, fetchCurrentBalance, {
    fallbackData: initialBalance ?? undefined,
    revalidateOnFocus: true
  });
  const { data: inviteSummary, mutate: mutateInviteSummary } = useSwrLite<InviteSummaryResponse>(
    API_PATHS.inviteSummary,
    fetchInviteSummary,
    {
      revalidateOnFocus: true
    }
  );

  const [balanceOverrideUsd, setBalanceOverrideUsd] = React.useState<number | null>(null);
  const [trackedTopupRequestId, setTrackedTopupRequestId] = React.useState<string | null>(null);
  const [topupBlocking, setTopupBlocking] = React.useState(false);
  const [topupPending, setTopupPending] = React.useState(false);
  const [checkingStatus, setCheckingStatus] = React.useState(false);

  function applyReportedBalance(nextBalance: number | null | undefined) {
    if (typeof nextBalance !== "number" || !Number.isFinite(nextBalance)) return;
    setBalanceOverrideUsd(nextBalance);
    void mutateSwrLite<number>(API_PATHS.authMe, nextBalance);
  }

  async function fetchTopupStatus(requestId: string, signal?: AbortSignal): Promise<BillingTopupStatusResponse> {
    const res = await fetch(billingTopupStatusApiPath(requestId), {
      cache: "no-store",
      signal
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error("Request failed");
    if (!isBillingTopupStatusResponse(json)) throw new Error("Invalid response");
    return json;
  }

  async function createTopupCheckout(amountUsd: number, paymentMethod: BillingPaymentMethod) {
    const res = await fetch(API_PATHS.billingTopupCheckout, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ amountUsd, paymentMethod })
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error("Request failed");
    if (!isBillingTopupCheckoutResponse(json)) throw new Error("Invalid response");
    return json;
  }

  const schema = React.useMemo(() => createTopupSchema(t), [t]);
  const amountUsdSchema = schema.shape.amountUsd;
  const form = useForm<TopupFormValues>({
    defaultValues: { amountUsd: 50, paymentMethod: "card" },
    mode: "onChange"
  });

  React.useEffect(() => {
    void form.trigger();
  }, [form]);

  const presetAmounts = React.useMemo(() => [10, 50, 100, 500], []);
  const paymentMethods = React.useMemo(
    () =>
      [
        { value: "card" as const, label: t("billing.topup.method.card") },
        { value: "alipay" as const, label: t("billing.topup.method.alipay") },
        { value: "wxpay" as const, label: t("billing.topup.method.wxpay") }
      ] satisfies ReadonlyArray<{ value: BillingPaymentMethod; label: string }>,
    [t]
  );
  const selectedPaymentMethod = form.watch("paymentMethod") ?? "card";

  const topupSubmitting = form.formState.isSubmitting;

  React.useEffect(() => {
    if (!topupRequestIdFromUrl) return;

    setTrackedTopupRequestId(topupRequestIdFromUrl);
    setTopupPending(false);
    setTopupBlocking(true);

    const controller = new AbortController();
    let cancelled = false;

    (async () => {
      const deadline = Date.now() + 10_000;
      while (!cancelled && Date.now() < deadline) {
        try {
          const status = await fetchTopupStatus(topupRequestIdFromUrl, controller.signal);
          if (cancelled) return;
          if (status.status === "completed") {
            setTopupBlocking(false);
            setTopupPending(false);
            clearTopupQueryParams();
            applyReportedBalance(status.newBalance);
            toast.success(t("billing.topup.toast.completed"));
            await mutateSwrLite<BillingLedgerItem[]>(key, undefined, {
              revalidate: true,
              dedupingIntervalMs: 0
            });
            void mutateCurrentBalance(undefined, { revalidate: true });
            void mutateSwrLite<InviteSummaryResponse>(API_PATHS.inviteSummary, undefined, {
              revalidate: true,
              dedupingIntervalMs: 0
            });
            return;
          }
          if (status.status === "failed") {
            setTopupBlocking(false);
            setTopupPending(false);
            clearTopupQueryParams();
            toast.error(t("billing.topup.toast.failed"));
            return;
          }
        } catch {
          break;
        }
        await waitMs(1200, controller.signal);
      }

      if (cancelled) return;
      setTopupBlocking(false);
      setTopupPending(true);
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [key, mutateCurrentBalance, t, topupRequestIdFromUrl]);

  async function recheckTopup() {
    if (!trackedTopupRequestId) return;
    if (checkingStatus) return;
    const startedAt = Date.now();
    setCheckingStatus(true);
    try {
      const status = await fetchTopupStatus(trackedTopupRequestId);
      if (status.status === "completed") {
        setTopupBlocking(false);
        setTopupPending(false);
        clearTopupQueryParams();
        applyReportedBalance(status.newBalance);
        toast.success(t("billing.topup.toast.completed"));
        await mutateSwrLite<BillingLedgerItem[]>(key, undefined, {
          revalidate: true,
          dedupingIntervalMs: 0
        });
        void mutateCurrentBalance(undefined, { revalidate: true });
        void mutateSwrLite<InviteSummaryResponse>(API_PATHS.inviteSummary, undefined, {
          revalidate: true,
          dedupingIntervalMs: 0
        });
      } else if (status.status === "failed") {
        setTopupBlocking(false);
        setTopupPending(false);
        clearTopupQueryParams();
        toast.error(t("billing.topup.toast.failed"));
      } else {
        setTopupPending(true);
      }
    } catch {
      toast.error(t("billing.topup.toast.statusFailed"));
    } finally {
      const elapsed = Date.now() - startedAt;
      const minMs = 700;
      if (elapsed < minMs) await waitMs(minMs - elapsed);
      setCheckingStatus(false);
    }
  }

  // Silent revalidate once after mount (no skeleton).
  React.useEffect(() => {
    if (!autoRevalidate) return;
    void mutate(undefined, { revalidate: true });
  }, [autoRevalidate, mutate]);

  React.useEffect(() => {
    void mutateCurrentBalance(undefined, { revalidate: true });
  }, [mutateCurrentBalance]);

  React.useEffect(() => {
    void mutateInviteSummary(undefined, { revalidate: true });
  }, [mutateInviteSummary]);

  React.useEffect(() => {
    const interval = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      void mutateCurrentBalance(fetchCurrentBalance(API_PATHS.authMe), { revalidate: false });
    }, 15_000);
    return () => window.clearInterval(interval);
  }, [mutateCurrentBalance]);

  const shouldShowSkeleton = data === undefined && initialItems === null;
  const items = data ?? initialItems ?? [];
  const currentBalanceUsd = currentBalanceData ?? initialBalance;
  const balanceUsd = balanceOverrideUsd ?? currentBalanceUsd;
  const pendingReceivedReward =
    inviteSummary?.receivedReward?.status === "pending" ? inviteSummary.receivedReward : null;

  React.useEffect(() => {
    if (balanceOverrideUsd === null) return;
    if (currentBalanceUsd === null) return;
    if (Math.abs(currentBalanceUsd - balanceOverrideUsd) > 0.0001) return;
    setBalanceOverrideUsd(null);
  }, [balanceOverrideUsd, currentBalanceUsd]);

  if (shouldShowSkeleton) return <BillingContentSkeleton topupEnabled={topupEnabled} />;

  async function onSubmit(values: TopupFormValues) {
    try {
      const checkout = await createTopupCheckout(values.amountUsd, values.paymentMethod);
      window.location.href = checkout.checkoutUrl;
    } catch {
      toast.error(t("billing.topup.toast.checkoutFailed"));
    }
  }

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard
          title={t("billing.kpi.balance")}
          value={balanceUsd === null ? "—" : formatUsdFixed2(balanceUsd, locale)}
          trend={t("billing.kpi.balanceHint")}
          icon={CreditCard}
          className={cn(topupEnabled ? "lg:col-span-2" : "sm:col-span-2 lg:col-span-4")}
        />

        {topupEnabled ? (
          <Card id="billing-topup" className="scroll-mt-24 lg:col-span-2">
            <CardHeader className="flex flex-row items-start justify-between gap-3">
              <div className="space-y-1">
                <CardTitle>{t("billing.topup.title")}</CardTitle>
                <CardDescription>{t("billing.topup.desc")}</CardDescription>
              </div>
            </CardHeader>
            <CardContent className="space-y-4">
              <form
                className="space-y-3"
                onSubmit={(e) => {
                  void form.handleSubmit(onSubmit)(e);
                }}
              >
                <input type="hidden" {...form.register("paymentMethod")} />
                <div className="space-y-2">
                  <div className="text-sm font-medium text-foreground">{t("billing.topup.methodLabel")}</div>
                  <div className="grid gap-2 sm:grid-cols-3">
                    {paymentMethods.map((method) => {
                      const active = selectedPaymentMethod === method.value;
                      return (
                        <Button
                          key={method.value}
                          type="button"
                          variant="outline"
                          className={cn(
                            "justify-start rounded-xl border-border/70 bg-background/40",
                            "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                            active
                              ? "border-primary/40 bg-primary/10 text-foreground shadow-[0_0_0_1px_oklch(var(--primary)/0.2),0_10px_24px_oklch(var(--primary)/0.12)]"
                              : "text-muted-foreground hover:border-primary/20 hover:bg-background/65"
                          )}
                          onClick={() =>
                            form.setValue("paymentMethod", method.value, {
                              shouldDirty: true,
                              shouldValidate: true
                            })
                          }
                          disabled={topupSubmitting || topupBlocking}
                        >
                          {method.label}
                        </Button>
                      );
                    })}
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
                  <div className="order-1 space-y-2">
                    <div className="text-sm font-medium text-foreground">{t("billing.topup.amountLabel")}</div>
                    <div className="relative">
                      <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-muted-foreground">
                        $
                      </span>
                      <Input
                        inputMode="numeric"
                        type="number"
                        min={5}
                        max={5000}
                        step={1}
                        className="pl-7"
                        disabled={topupSubmitting}
                        {...form.register("amountUsd", {
                          setValueAs: (value) => {
                            if (value == null || value === "") return 0;
                            const parsed = Number(value);
                            return Number.isFinite(parsed) ? parsed : 0;
                          },
                          validate: (value) => {
                            const r = amountUsdSchema.safeParse(value);
                            return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                          }
                        })}
                      />
                    </div>
                  </div>

                  <Button
                    type="submit"
                    className={cn(
                      "order-3 rounded-xl sm:order-2 sm:self-end",
                      "transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg"
                    )}
                    disabled={!form.formState.isValid || topupSubmitting || topupBlocking}
                  >
                    {topupSubmitting ? (
                      <>
                        <span className="inline-flex animate-spin">
                          <Loader2 className="h-4 w-4" />
                        </span>
                        {t("billing.topup.submitting")}
                      </>
                    ) : (
                      <>
                        <ArrowUpRight className="h-4 w-4" />
                        {t("billing.topup.submit")}
                      </>
                    )}
                  </Button>
                </div>
                <p
                  className={cn(
                    "order-2 text-xs",
                    form.formState.errors.amountUsd ? "text-destructive" : "text-muted-foreground",
                    "sm:order-3 sm:col-span-2"
                  )}
                >
                  {form.formState.errors.amountUsd?.message ?? t("billing.topup.amountHelp")}
                </p>

                <div className="flex flex-wrap gap-2">
                  {presetAmounts.map((amount) => (
                    <Button
                      key={amount}
                      type="button"
                      variant="secondary"
                      className="h-8 rounded-xl px-3 text-xs"
                      onClick={() => form.setValue("amountUsd", amount, { shouldValidate: true })}
                      disabled={topupSubmitting || topupBlocking}
                    >
                      ${amount}
                    </Button>
                  ))}
                </div>

              </form>

              {topupBlocking ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <span className="inline-flex animate-spin">
                    <Loader2 className="h-4 w-4" />
                  </span>
                  {t("billing.topup.checking")}
                </div>
              ) : topupPending && trackedTopupRequestId ? (
                <div className="flex items-start justify-between gap-4 rounded-xl border border-border bg-muted/10 p-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-foreground">{t("billing.topup.processing")}</span>
                      <span className="font-mono text-xs text-muted-foreground">
                        {trackedTopupRequestId.slice(0, 10)}…
                      </span>
                    </div>
                    <div className="text-sm text-muted-foreground">{t("billing.topup.processingDesc")}</div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="shrink-0 rounded-xl"
                    onClick={() => void recheckTopup()}
                    disabled={checkingStatus}
                  >
                    <span className={cn("inline-flex", checkingStatus ? "animate-spin" : null)}>
                      {checkingStatus ? <Loader2 className="h-4 w-4" /> : <RefreshCw className="h-4 w-4" />}
                    </span>
                    {checkingStatus ? t("billing.topup.checkingShort") : t("billing.topup.recheck")}
                  </Button>
                </div>
              ) : null}
            </CardContent>
          </Card>
        ) : null}
      </div>

      {pendingReceivedReward ? (
        <Card className="overflow-hidden border-warning/25 bg-warning/5">
          <CardContent className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-start gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-warning/25 bg-warning/15 text-warning">
                <Gift className="h-4 w-4" />
              </div>
              <div className="min-w-0 space-y-1">
                <div className="text-sm font-medium text-foreground">{t("invite.received.title")}</div>
                <div className="text-sm text-muted-foreground">{t("invite.received.desc.pending")}</div>
              </div>
            </div>
            <div className="flex shrink-0 flex-col items-start gap-2 sm:items-end">
              <Badge variant="warning">{t("invite.status.pending")}</Badge>
              <div className="font-mono text-lg font-semibold tabular-nums text-foreground">
                {typeof pendingReceivedReward.rewardUsd === "number" && Number.isFinite(pendingReceivedReward.rewardUsd)
                  ? formatUsdFixed2(pendingReceivedReward.rewardUsd, locale)
                  : "—"}
              </div>
              {pendingReceivedReward.availableAt ? (
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Clock className="h-3.5 w-3.5" />
                  <span>{t("invite.received.availableAt")}</span>
                  <span className="font-mono tabular-nums text-foreground">
                    <ClientDateTime value={pendingReceivedReward.availableAt} locale={locale} timeStyle="medium" />
                  </span>
                </div>
              ) : null}
            </div>
          </CardContent>
        </Card>
      ) : null}

      <BillingTableClient initialItems={items} locale={locale} />
    </div>
  );
}
