"use client";

import * as React from "react";
import { z } from "zod";
import { ArrowUpRight, CreditCard, Loader2, RefreshCw } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { StatsCard } from "@/components/app/stats-card";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { BillingTableClient } from "./billing-table-client";
import { API_PATHS, billingLedgerListApiPath, billingTopupStatusApiPath } from "@/lib/api-paths";
import { formatUsdFixed2 } from "@/lib/format";
import { mutateSwrLite, useSwrLite } from "@/lib/swr-lite";
import type { BillingLedgerItem, BillingLedgerListResponse, BillingTopupCheckoutResponse, BillingTopupStatusResponse } from "@/lib/types";
import type { Locale, MessageKey, MessageVars } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { BillingContentSkeleton } from "./billing-skeleton";

function isBillingLedgerListResponse(value: unknown): value is BillingLedgerListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function fetchLedger(key: string) {
  const res = await fetch(key, { cache: "no-store" });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) throw new Error("Request failed");
  if (!isBillingLedgerListResponse(json)) throw new Error("Invalid response");
  return json.items;
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
      .max(5000, t("billing.topup.validation.max", { max: 5000 }))
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
  url.searchParams.delete("request_id");
  url.searchParams.delete("requestId");
  if (url.toString() === window.location.href) return;
  window.history.replaceState(null, "", url.toString());
}

interface BillingContentClientProps {
  locale: Locale;
  initialItems: BillingLedgerItem[] | null;
  pageSize: number;
  topupEnabled: boolean;
  autoRevalidate?: boolean;
}

export function BillingContentClient({
  locale,
  initialItems,
  pageSize,
  topupEnabled,
  autoRevalidate = true
}: BillingContentClientProps) {
  const { t } = useI18n();
  const searchParams = useSearchParams();

  const topupRequestIdFromUrl = React.useMemo(() => {
    const raw = searchParams.get("request_id") ?? searchParams.get("requestId");
    if (!raw) return null;
    const trimmed = raw.trim();
    return trimmed.length > 0 ? trimmed : null;
  }, [searchParams]);

  const key = billingLedgerListApiPath(pageSize, 0);
  const { data, mutate } = useSwrLite<BillingLedgerItem[]>(key, fetchLedger, {
    fallbackData: initialItems ?? undefined,
    revalidateOnFocus: false
  });

  const [balanceOverrideUsd, setBalanceOverrideUsd] = React.useState<number | null>(null);
  const [trackedTopupRequestId, setTrackedTopupRequestId] = React.useState<string | null>(null);
  const [topupBlocking, setTopupBlocking] = React.useState(false);
  const [topupPending, setTopupPending] = React.useState(false);
  const [checkingStatus, setCheckingStatus] = React.useState(false);

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

  async function createTopupCheckout(amountUsd: number) {
    const res = await fetch(API_PATHS.billingTopupCheckout, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ amountUsd })
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error("Request failed");
    if (!isBillingTopupCheckoutResponse(json)) throw new Error("Invalid response");
    return json;
  }

  const schema = React.useMemo(() => createTopupSchema(t), [t]);
  const amountUsdSchema = schema.shape.amountUsd;
  const form = useForm<TopupFormValues>({
    defaultValues: { amountUsd: 50 },
    mode: "onChange"
  });

  React.useEffect(() => {
    void form.trigger();
  }, [form]);

  const presetAmounts = React.useMemo(() => [10, 50, 100, 500], []);

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
            if (typeof status.newBalance === "number") setBalanceOverrideUsd(status.newBalance);
            toast.success(t("billing.topup.toast.completed"));
            await mutateSwrLite<BillingLedgerItem[]>(key, undefined, {
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
  }, [key, t, topupRequestIdFromUrl]);

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
        if (typeof status.newBalance === "number") setBalanceOverrideUsd(status.newBalance);
        toast.success(t("billing.topup.toast.completed"));
        await mutateSwrLite<BillingLedgerItem[]>(key, undefined, {
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

  const shouldShowSkeleton = data === undefined && initialItems === null;
  const items = data ?? initialItems ?? [];
  const ledgerBalanceUsd = items.length > 0 ? Number(items[0]?.balanceUsd ?? 0) : 0;
  const balanceUsd = balanceOverrideUsd ?? ledgerBalanceUsd;

  React.useEffect(() => {
    if (balanceOverrideUsd === null) return;
    if (Math.abs(ledgerBalanceUsd - balanceOverrideUsd) > 0.0001) return;
    setBalanceOverrideUsd(null);
  }, [balanceOverrideUsd, ledgerBalanceUsd]);

  if (shouldShowSkeleton) return <BillingContentSkeleton topupEnabled={topupEnabled} />;

  async function onSubmit(values: TopupFormValues) {
    try {
      const checkout = await createTopupCheckout(values.amountUsd);
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
          value={formatUsdFixed2(balanceUsd, locale)}
          trend={t("billing.kpi.balanceHint")}
          icon={CreditCard}
          className={cn(topupEnabled ? "lg:col-span-2" : "sm:col-span-2 lg:col-span-4")}
        />

        {topupEnabled ? (
          <Card className="lg:col-span-2">
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

      <BillingTableClient initialItems={items} locale={locale} />
    </div>
  );
}
