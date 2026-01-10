import { Settings } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent } from "@/components/ui/card";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import { AdminSettingsPanel } from "./settings-panel";

interface AdminSettingsResponse {
  registrationEnabled: boolean;
}

function isAdminSettingsResponse(value: unknown): value is AdminSettingsResponse {
  if (!value || typeof value !== "object") return false;
  return typeof (value as { registrationEnabled?: unknown }).registrationEnabled === "boolean";
}

async function getAdminSettings() {
  const res = await fetch(buildBackendUrl("/admin/settings"), {
    cache: "force-cache",
    next: { tags: ["admin:settings"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  if (!isAdminSettingsResponse(json)) return null;
  return json;
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
            icon={<Settings className="h-6 w-6 text-muted-foreground uai-float-sm" />}
            title={t(locale, "admin.settings.loadFailed")}
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardContent className="p-6">
        <AdminSettingsPanel initialRegistrationEnabled={settings.registrationEnabled} />
      </CardContent>
    </Card>
  );
}
