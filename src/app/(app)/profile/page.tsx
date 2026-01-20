import { Shield, User } from "lucide-react";

import { DeleteAccountButton } from "@/components/profile/delete-account-button";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { getCurrentUser } from "@/lib/current-user";

export const dynamic = "force-dynamic";

function roleVariant(role: string): "outline" | "success" | "warning" {
  if (role === "owner") return "warning";
  if (role === "admin") return "success";
  return "outline";
}

export default async function ProfilePage() {
  const [locale, me] = await Promise.all([getRequestLocale(), getCurrentUser()]);

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "profile.title")} description={t(locale, "profile.subtitle")} />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <User className="h-5 w-5 text-muted-foreground" />
            {t(locale, "profile.card.title")}
          </CardTitle>
          <CardDescription>{t(locale, "profile.card.desc")}</CardDescription>
        </CardHeader>
        <CardContent>
          {!me ? (
            <EmptyState
              icon={(
                <span className="inline-flex uai-float-sm">
                  <User className="h-6 w-6 text-muted-foreground" />
                </span>
              )}
              title={t(locale, "profile.error")}
            />
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.email")}</div>
                <div className="mt-1 truncate text-sm font-medium text-foreground">{me.email}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.role")}</div>
                <div className="mt-1 flex items-center gap-2">
                  <Badge variant={roleVariant(me.role)}>{me.role}</Badge>
                  <Shield className="h-4 w-4 text-muted-foreground" />
                </div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.group")}</div>
                <div className="mt-1 font-mono text-xs text-foreground">{me.group}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.balance")}</div>
                <div className="mt-1 font-mono tabular-nums text-sm text-foreground">{me.balance}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.created")}</div>
                <div className="mt-1 text-xs text-foreground">
                  <ClientDateTime value={me.createdAt} locale={locale} />
                </div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.lastLogin")}</div>
                <div className="mt-1 text-xs text-foreground">
                  <ClientDateTime value={me.lastLoginAt} locale={locale} />
                </div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4 sm:col-span-2">
                <div className="text-xs text-muted-foreground">{t(locale, "profile.field.workspace")}</div>
                <div className="mt-1 font-mono text-xs text-foreground">{me.orgId}</div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="border-destructive/40 bg-destructive/5">
        <CardHeader>
          <CardTitle>{t(locale, "profile.danger.title")}</CardTitle>
          <CardDescription>{t(locale, "profile.danger.desc")}</CardDescription>
        </CardHeader>
        <CardContent className="flex items-center justify-between gap-4">
          <div className="text-sm text-muted-foreground">
            {t(locale, "profile.danger.body")}
          </div>
          <DeleteAccountButton className="rounded-xl" />
        </CardContent>
      </Card>
    </div>
  );
}
