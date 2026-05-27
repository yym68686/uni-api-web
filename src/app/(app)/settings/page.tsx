import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { SettingsPanel } from "./_components/settings-panel";

export default async function SettingsPage() {
  const locale = await getRequestLocale();

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "settings.title")} description={t(locale, "settings.subtitle")} />
      <SettingsPanel />
    </div>
  );
}
