"use client";

import * as React from "react";
import { DollarSign, Loader2, Save } from "lucide-react";
import { toast } from "sonner";

import { useI18n } from "@/components/i18n/i18n-provider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { AdminSettingsResponse, AdminSettingsUpdateRequest } from "@/lib/types";
import { cn } from "@/lib/utils";

interface AdminSettingsPanelProps {
  initialRegistrationEnabled: boolean;
  initialBillingTopupEnabled: boolean;
  initialBillingPaymentCardEnabled: boolean;
  initialBillingPaymentAlipayEnabled: boolean;
  initialBillingPaymentWxpayEnabled: boolean;
  initialNewUserTrialEnabled: boolean;
  initialNewUserTrialBalance: number;
  className?: string;
}

type BooleanAdminSettingsKey =
  | "registrationEnabled"
  | "billingTopupEnabled"
  | "billingPaymentCardEnabled"
  | "billingPaymentAlipayEnabled"
  | "billingPaymentWxpayEnabled"
  | "newUserTrialEnabled";
type SavingKey = BooleanAdminSettingsKey | "newUserTrialBalance";

interface SettingRow {
  key: BooleanAdminSettingsKey;
  label: string;
  desc: string;
}

function normalizeAdminSettingsResponse(
  value: unknown,
  previous: AdminSettingsResponse
): AdminSettingsResponse | null {
  if (!value || typeof value !== "object") return null;
  const obj = value as Record<string, unknown>;

  const registrationEnabled = obj.registrationEnabled ?? obj.registration_enabled;
  if (typeof registrationEnabled !== "boolean") return null;

  const billingTopupEnabledRaw = obj.billingTopupEnabled ?? obj.billing_topup_enabled;
  const billingPaymentCardEnabledRaw = obj.billingPaymentCardEnabled ?? obj.billing_payment_card_enabled;
  const billingPaymentAlipayEnabledRaw = obj.billingPaymentAlipayEnabled ?? obj.billing_payment_alipay_enabled;
  const billingPaymentWxpayEnabledRaw = obj.billingPaymentWxpayEnabled ?? obj.billing_payment_wxpay_enabled;
  const newUserTrialEnabledRaw = obj.newUserTrialEnabled ?? obj.new_user_trial_enabled;
  const newUserTrialBalanceRaw = obj.newUserTrialBalance ?? obj.new_user_trial_balance;
  const newUserTrialBalance =
    typeof newUserTrialBalanceRaw === "number" && Number.isFinite(newUserTrialBalanceRaw)
      ? newUserTrialBalanceRaw
      : previous.newUserTrialBalance;

  return {
    registrationEnabled,
    billingTopupEnabled:
      typeof billingTopupEnabledRaw === "boolean" ? billingTopupEnabledRaw : previous.billingTopupEnabled,
    billingPaymentCardEnabled:
      typeof billingPaymentCardEnabledRaw === "boolean"
        ? billingPaymentCardEnabledRaw
        : previous.billingPaymentCardEnabled,
    billingPaymentAlipayEnabled:
      typeof billingPaymentAlipayEnabledRaw === "boolean"
        ? billingPaymentAlipayEnabledRaw
        : previous.billingPaymentAlipayEnabled,
    billingPaymentWxpayEnabled:
      typeof billingPaymentWxpayEnabledRaw === "boolean"
        ? billingPaymentWxpayEnabledRaw
        : previous.billingPaymentWxpayEnabled,
    newUserTrialEnabled:
      typeof newUserTrialEnabledRaw === "boolean" ? newUserTrialEnabledRaw : previous.newUserTrialEnabled,
    newUserTrialBalance
  };
}

function createUpdatePayload(key: BooleanAdminSettingsKey, value: boolean): AdminSettingsUpdateRequest {
  switch (key) {
    case "registrationEnabled":
      return { registrationEnabled: value };
    case "billingTopupEnabled":
      return { billingTopupEnabled: value };
    case "billingPaymentCardEnabled":
      return { billingPaymentCardEnabled: value };
    case "billingPaymentAlipayEnabled":
      return { billingPaymentAlipayEnabled: value };
    case "billingPaymentWxpayEnabled":
      return { billingPaymentWxpayEnabled: value };
    case "newUserTrialEnabled":
      return { newUserTrialEnabled: value };
  }
}

function formatTrialBalanceInput(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return value.toFixed(2).replace(/\.00$/, "");
}

function parseTrialBalanceInput(value: string): number | null {
  const raw = value.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(raw)) return null;
  const next = Number(raw);
  if (!Number.isFinite(next)) return null;
  if (next < 0 || next > 1_000_000) return null;
  return Math.round(next * 100) / 100;
}

export function AdminSettingsPanel({
  initialRegistrationEnabled,
  initialBillingTopupEnabled,
  initialBillingPaymentCardEnabled,
  initialBillingPaymentAlipayEnabled,
  initialBillingPaymentWxpayEnabled,
  initialNewUserTrialEnabled,
  initialNewUserTrialBalance,
  className
}: AdminSettingsPanelProps) {
  const { t } = useI18n();
  const [settings, setSettings] = React.useState<AdminSettingsResponse>({
    registrationEnabled: initialRegistrationEnabled,
    billingTopupEnabled: initialBillingTopupEnabled,
    billingPaymentCardEnabled: initialBillingPaymentCardEnabled,
    billingPaymentAlipayEnabled: initialBillingPaymentAlipayEnabled,
    billingPaymentWxpayEnabled: initialBillingPaymentWxpayEnabled,
    newUserTrialEnabled: initialNewUserTrialEnabled,
    newUserTrialBalance: initialNewUserTrialBalance
  });
  const [trialBalanceInput, setTrialBalanceInput] = React.useState(formatTrialBalanceInput(initialNewUserTrialBalance));
  const [savingKey, setSavingKey] = React.useState<SavingKey | null>(null);
  const saving = savingKey !== null;

  function applyNormalizedSettings(next: AdminSettingsResponse) {
    setSettings(next);
    setTrialBalanceInput(formatTrialBalanceInput(next.newUserTrialBalance));
  }

  async function updateSetting(key: BooleanAdminSettingsKey, nextValue: boolean) {
    const previous = settings;
    setSettings((current) => ({ ...current, [key]: nextValue }));
    setSavingKey(key);

    try {
      const res = await fetch("/api/admin/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(createUpdatePayload(key, nextValue))
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("admin.settings.toast.failed"))
            : t("admin.settings.toast.failed");
        throw new Error(message);
      }

      const normalized = normalizeAdminSettingsResponse(json, previous);
      if (normalized) {
        applyNormalizedSettings(normalized);
      }
      toast.success(t("admin.settings.toast.updated"));
    } catch (err) {
      setSettings(previous);
      setTrialBalanceInput(formatTrialBalanceInput(previous.newUserTrialBalance));
      toast.error(err instanceof Error ? err.message : t("admin.settings.toast.failed"));
    } finally {
      setSavingKey(null);
    }
  }

  async function updateTrialBalance() {
    const nextValue = parseTrialBalanceInput(trialBalanceInput);
    if (nextValue === null) {
      toast.error(t("admin.settings.newUserTrial.invalid"));
      return;
    }

    const previous = settings;
    setSettings((current) => ({ ...current, newUserTrialBalance: nextValue }));
    setSavingKey("newUserTrialBalance");

    try {
      const res = await fetch("/api/admin/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ newUserTrialBalance: nextValue })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("admin.settings.toast.failed"))
            : t("admin.settings.toast.failed");
        throw new Error(message);
      }

      const normalized = normalizeAdminSettingsResponse(json, previous);
      if (normalized) {
        applyNormalizedSettings(normalized);
      }
      toast.success(t("admin.settings.toast.updated"));
    } catch (err) {
      setSettings(previous);
      setTrialBalanceInput(formatTrialBalanceInput(previous.newUserTrialBalance));
      toast.error(err instanceof Error ? err.message : t("admin.settings.toast.failed"));
    } finally {
      setSavingKey(null);
    }
  }

  const coreRows = [
    {
      key: "registrationEnabled",
      label: t("admin.settings.registration.label"),
      desc: t("admin.settings.registration.desc")
    },
    {
      key: "billingTopupEnabled",
      label: t("admin.settings.billingTopup.label"),
      desc: t("admin.settings.billingTopup.desc")
    }
  ] satisfies ReadonlyArray<SettingRow>;

  const trialRow = {
    key: "newUserTrialEnabled",
    label: t("admin.settings.newUserTrial.label"),
    desc: t("admin.settings.newUserTrial.desc")
  } satisfies SettingRow;

  const paymentRows = [
    {
      key: "billingPaymentCardEnabled",
      label: t("admin.settings.paymentMethods.card.label"),
      desc: t("admin.settings.paymentMethods.card.desc")
    },
    {
      key: "billingPaymentAlipayEnabled",
      label: t("admin.settings.paymentMethods.alipay.label"),
      desc: t("admin.settings.paymentMethods.alipay.desc")
    },
    {
      key: "billingPaymentWxpayEnabled",
      label: t("admin.settings.paymentMethods.wxpay.label"),
      desc: t("admin.settings.paymentMethods.wxpay.desc")
    }
  ] satisfies ReadonlyArray<SettingRow>;

  function renderSettingRow(row: SettingRow) {
    return (
      <div key={row.key} className="flex items-start justify-between gap-4">
        <div className="space-y-1.5">
          <div className="text-sm font-medium text-foreground">{row.label}</div>
          <div className="text-sm text-muted-foreground">{row.desc}</div>
        </div>
        <div className="flex items-center gap-3">
          {savingKey === row.key ? (
            <span className="inline-flex animate-spin text-muted-foreground">
              <Loader2 className="h-4 w-4" />
            </span>
          ) : null}
          <Switch
            checked={settings[row.key]}
            disabled={saving}
            onCheckedChange={(v) => {
              void updateSetting(row.key, v);
            }}
            aria-label={row.label}
          />
        </div>
      </div>
    );
  }

  const trialBalanceChanged = parseTrialBalanceInput(trialBalanceInput) !== settings.newUserTrialBalance;

  return (
    <div className={cn("rounded-xl border border-border bg-muted/10 p-4", className)}>
      <div className="space-y-6">
        {coreRows.map(renderSettingRow)}

        <div className="space-y-4 border-t border-border pt-5">
          {renderSettingRow(trialRow)}
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
            <div className="space-y-2">
              <Label htmlFor="new-user-trial-balance">{t("admin.settings.newUserTrial.amountLabel")}</Label>
              <div className="relative">
                <DollarSign className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  id="new-user-trial-balance"
                  className="pl-9 font-mono tabular-nums"
                  inputMode="decimal"
                  min="0"
                  max="1000000"
                  step="0.01"
                  value={trialBalanceInput}
                  disabled={!settings.newUserTrialEnabled || saving}
                  onChange={(event) => setTrialBalanceInput(event.target.value)}
                />
              </div>
              <p className="text-xs text-muted-foreground">{t("admin.settings.newUserTrial.amountHelp")}</p>
            </div>
            <Button
              type="button"
              className="shrink-0 rounded-xl"
              disabled={!settings.newUserTrialEnabled || saving || !trialBalanceChanged}
              onClick={() => {
                void updateTrialBalance();
              }}
            >
              {savingKey === "newUserTrialBalance" ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Save className="h-4 w-4" />
              )}
              {t("admin.settings.newUserTrial.save")}
            </Button>
          </div>
        </div>

        <div className="space-y-4 border-t border-border pt-5">
          <div>
            <div className="text-sm font-medium text-foreground">{t("admin.settings.paymentMethods.label")}</div>
            <div className="text-sm text-muted-foreground">{t("admin.settings.paymentMethods.desc")}</div>
          </div>
          {paymentRows.map(renderSettingRow)}
        </div>
      </div>
    </div>
  );
}
