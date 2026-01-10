"use client";

import * as React from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";

interface AdminSettingsPanelProps {
  initialRegistrationEnabled: boolean;
  className?: string;
}

interface AdminSettingsResponse {
  registrationEnabled: boolean;
}

function isAdminSettingsResponse(value: unknown): value is AdminSettingsResponse {
  if (!value || typeof value !== "object") return false;
  return typeof (value as { registrationEnabled?: unknown }).registrationEnabled === "boolean";
}

export function AdminSettingsPanel({ initialRegistrationEnabled, className }: AdminSettingsPanelProps) {
  const { t } = useI18n();
  const [registrationEnabled, setRegistrationEnabled] = React.useState(initialRegistrationEnabled);
  const [saving, setSaving] = React.useState(false);

  async function updateRegistrationEnabled(nextValue: boolean) {
    const previous = registrationEnabled;
    setRegistrationEnabled(nextValue);
    setSaving(true);
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
      if (isAdminSettingsResponse(json)) {
        setRegistrationEnabled(json.registrationEnabled);
      }
      toast.success(t("admin.settings.toast.updated"));
    } catch (err) {
      setRegistrationEnabled(previous);
      toast.error(err instanceof Error ? err.message : t("admin.settings.toast.failed"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className={cn("rounded-xl border border-border bg-muted/10 p-4", className)}>
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1.5">
          <div className="text-sm font-medium text-foreground">{t("admin.settings.registration.label")}</div>
          <div className="text-sm text-muted-foreground">{t("admin.settings.registration.desc")}</div>
        </div>
        <div className="flex items-center gap-3">
          {saving ? <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" /> : null}
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
    </div>
  );
}

