"use client";

import * as React from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";

interface AdminSettingsPanelProps {
  initialRegistrationEnabled: boolean;
  initialBillingTopupEnabled: boolean;
  className?: string;
}

interface AdminSettingsResponse {
  registrationEnabled: boolean;
  billingTopupEnabled: boolean;
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
  const billingTopupEnabled =
    typeof billingTopupEnabledRaw === "boolean" ? billingTopupEnabledRaw : previous.billingTopupEnabled;

  return { registrationEnabled, billingTopupEnabled };
}

export function AdminSettingsPanel({
  initialRegistrationEnabled,
  initialBillingTopupEnabled,
  className
}: AdminSettingsPanelProps) {
  const { t } = useI18n();
  const [registrationEnabled, setRegistrationEnabled] = React.useState(initialRegistrationEnabled);
  const [billingTopupEnabled, setBillingTopupEnabled] = React.useState(initialBillingTopupEnabled);
  const [savingKey, setSavingKey] = React.useState<"registration" | "billingTopup" | null>(null);
  const saving = savingKey !== null;

  async function updateRegistrationEnabled(nextValue: boolean) {
    const previous = registrationEnabled;
    setRegistrationEnabled(nextValue);
    setSavingKey("registration");
    try {
      const res = await fetch("/api/admin/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ registrationEnabled: nextValue })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("admin.settings.toast.failed"))
            : t("admin.settings.toast.failed");
        throw new Error(message);
      }
      const normalized = normalizeAdminSettingsResponse(json, { registrationEnabled, billingTopupEnabled });
      if (normalized) {
        setRegistrationEnabled(normalized.registrationEnabled);
        setBillingTopupEnabled(normalized.billingTopupEnabled);
      }
      toast.success(t("admin.settings.toast.updated"));
    } catch (err) {
      setRegistrationEnabled(previous);
      toast.error(err instanceof Error ? err.message : t("admin.settings.toast.failed"));
    } finally {
      setSavingKey(null);
    }
  }

  async function updateBillingTopupEnabled(nextValue: boolean) {
    const previous = billingTopupEnabled;
    setBillingTopupEnabled(nextValue);
    setSavingKey("billingTopup");
    try {
      const res = await fetch("/api/admin/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ billingTopupEnabled: nextValue })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("admin.settings.toast.failed"))
            : t("admin.settings.toast.failed");
        throw new Error(message);
      }
      const normalized = normalizeAdminSettingsResponse(json, { registrationEnabled, billingTopupEnabled });
      if (normalized) {
        setRegistrationEnabled(normalized.registrationEnabled);
        setBillingTopupEnabled(normalized.billingTopupEnabled);
      }
      toast.success(t("admin.settings.toast.updated"));
    } catch (err) {
      setBillingTopupEnabled(previous);
      toast.error(err instanceof Error ? err.message : t("admin.settings.toast.failed"));
    } finally {
      setSavingKey(null);
    }
  }

  return (
    <div className={cn("rounded-xl border border-border bg-muted/10 p-4", className)}>
      <div className="space-y-6">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1.5">
            <div className="text-sm font-medium text-foreground">{t("admin.settings.registration.label")}</div>
            <div className="text-sm text-muted-foreground">{t("admin.settings.registration.desc")}</div>
          </div>
          <div className="flex items-center gap-3">
            {savingKey === "registration" ? (
              <span className="inline-flex animate-spin text-muted-foreground">
                <Loader2 className="h-4 w-4" />
              </span>
            ) : null}
            <Switch
              checked={registrationEnabled}
              disabled={saving}
              onCheckedChange={(v) => {
                void updateRegistrationEnabled(v);
              }}
              aria-label={t("admin.settings.registration.label")}
            />
          </div>
        </div>

        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1.5">
            <div className="text-sm font-medium text-foreground">{t("admin.settings.billingTopup.label")}</div>
            <div className="text-sm text-muted-foreground">{t("admin.settings.billingTopup.desc")}</div>
          </div>
          <div className="flex items-center gap-3">
            {savingKey === "billingTopup" ? (
              <span className="inline-flex animate-spin text-muted-foreground">
                <Loader2 className="h-4 w-4" />
              </span>
            ) : null}
            <Switch
              checked={billingTopupEnabled}
              disabled={saving}
              onCheckedChange={(v) => {
                void updateBillingTopupEnabled(v);
              }}
              aria-label={t("admin.settings.billingTopup.label")}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
