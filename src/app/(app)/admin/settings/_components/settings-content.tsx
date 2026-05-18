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

interface AdminDataOceanStatus {
  enabled: boolean;
  collectUrl?: string | null;
  projectId: string;
  dashboardUrl?: string | null;
  serverKeyConfigured: boolean;
  total: number;
  pending: number;
  failed: number;
  sent: number;
  lastSentAt?: string | null;
  lastQueuedAt?: string | null;
  lastError?: string | null;
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

async function getDataOceanStatus() {
  const res = await fetch(buildBackendUrl("/admin/dataocean/status"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.adminDataOcean] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  if (!json || typeof json !== "object") return null;
  const obj = json as Record<string, unknown>;
  return {
    enabled: Boolean(obj.enabled),
    collectUrl: typeof obj.collectUrl === "string" ? obj.collectUrl : null,
    projectId: typeof obj.projectId === "string" ? obj.projectId : "uni-api-web",
    dashboardUrl: typeof obj.dashboardUrl === "string" ? obj.dashboardUrl : null,
    serverKeyConfigured: Boolean(obj.serverKeyConfigured),
    total: Number(obj.total ?? 0),
    pending: Number(obj.pending ?? 0),
    failed: Number(obj.failed ?? 0),
    sent: Number(obj.sent ?? 0),
    lastSentAt: typeof obj.lastSentAt === "string" ? obj.lastSentAt : null,
    lastQueuedAt: typeof obj.lastQueuedAt === "string" ? obj.lastQueuedAt : null,
    lastError: typeof obj.lastError === "string" ? obj.lastError : null
  } satisfies AdminDataOceanStatus;
}

interface AdminSettingsContentProps {
  locale: Locale;
}

export async function AdminSettingsContent({ locale }: AdminSettingsContentProps) {
  const [settings, dataOceanStatus] = await Promise.all([getAdminSettings(), getDataOceanStatus()]);

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
    <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card>
        <CardContent className="p-6">
          <AdminSettingsPanel
            initialRegistrationEnabled={settings.registrationEnabled}
            initialBillingTopupEnabled={settings.billingTopupEnabled}
          />
        </CardContent>
      </Card>

      <Card>
        <CardContent className="p-6">
          <div className="space-y-4">
            <div>
              <div className="text-sm font-medium text-foreground">{t(locale, "admin.settings.dataocean.title")}</div>
              <div className="text-sm text-muted-foreground">{t(locale, "admin.settings.dataocean.desc")}</div>
            </div>
            <div className="grid gap-3">
              <InfoRow label={t(locale, "admin.settings.dataocean.enabled")} value={dataOceanStatus?.enabled ? t(locale, "common.enabled") : t(locale, "common.disabled")} />
              <InfoRow label={t(locale, "admin.settings.dataocean.project")} value={dataOceanStatus?.projectId ?? "-"} />
              <InfoRow label={t(locale, "admin.settings.dataocean.pending")} value={String(dataOceanStatus?.pending ?? 0)} />
              <InfoRow label={t(locale, "admin.settings.dataocean.failed")} value={String(dataOceanStatus?.failed ?? 0)} />
              <InfoRow label={t(locale, "admin.settings.dataocean.sent")} value={String(dataOceanStatus?.sent ?? 0)} />
              <InfoRow label={t(locale, "admin.settings.dataocean.lastSent")} value={dataOceanStatus?.lastSentAt ?? "-"} />
              <InfoRow label={t(locale, "admin.settings.dataocean.keys")} value={dataOceanStatus?.serverKeyConfigured ? t(locale, "common.enabled") : t(locale, "common.disabled")} />
              {dataOceanStatus?.dashboardUrl ? (
                <a
                  className="text-sm font-medium text-primary hover:underline"
                  href={dataOceanStatus.dashboardUrl}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t(locale, "admin.settings.dataocean.open")}
                </a>
              ) : null}
            </div>
            {dataOceanStatus?.lastError ? (
              <div className="rounded-lg border border-border bg-muted/20 p-3 text-xs text-muted-foreground">
                {dataOceanStatus.lastError}
              </div>
            ) : null}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/10 px-3 py-2">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-sm font-medium text-foreground">{value}</span>
    </div>
  );
}
