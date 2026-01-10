import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
    cache: "no-store",
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
        <CardHeader>
          <CardTitle>{t(locale, "admin.settings.card.title")}</CardTitle>
          <CardDescription>{t(locale, "admin.settings.loadFailed")}</CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(locale, "admin.settings.card.title")}</CardTitle>
        <CardDescription>{t(locale, "admin.settings.card.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        <AdminSettingsPanel initialRegistrationEnabled={settings.registrationEnabled} />
      </CardContent>
    </Card>
  );
}

