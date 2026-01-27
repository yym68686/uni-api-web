import { Settings } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent } from "@/components/ui/card";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { AdminSettingsPanel } from "./settings-panel";

interface AdminSettingsResponse {
  registrationEnabled: boolean;
  billingTopupEnabled: boolean;
}

function normalizeAdminSettingsResponse(value: unknown): AdminSettingsResponse | null {
  if (!value || typeof value !== "object") return null;
  const obj = value as Record<string, unknown>;

  const registrationEnabled = obj.registrationEnabled ?? obj.registration_enabled;
  if (typeof registrationEnabled !== "boolean") return null;

  const billingTopupEnabledRaw = obj.billingTopupEnabled ?? obj.billing_topup_enabled;
  const billingTopupEnabled = typeof billingTopupEnabledRaw === "boolean" ? billingTopupEnabledRaw : true;

  return { registrationEnabled, billingTopupEnabled };
}

async function getAdminSettings() {
  const res = await fetch(buildBackendUrl("/admin/settings"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.adminSettings] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  return normalizeAdminSettingsResponse(json);
}

interface AdminSettingsContentProps {
  locale: Locale;
}

export async function AdminSettingsContent({ locale }: AdminSettingsContentProps) {
  const settings = await getAdminSettings();

  if (!settings) {
    return (
      <Card>
        <CardContent className="p-6">
          <EmptyState
            icon={(
              <span className="inline-flex uai-float-sm">
                <Settings className="h-6 w-6 text-muted-foreground" />
              </span>
            )}
            title={t(locale, "admin.settings.loadFailed")}
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardContent className="p-6">
        <AdminSettingsPanel
          initialRegistrationEnabled={settings.registrationEnabled}
          initialBillingTopupEnabled={settings.billingTopupEnabled}
        />
      </CardContent>
    </Card>
  );
}
