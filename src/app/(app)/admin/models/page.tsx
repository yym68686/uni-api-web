import { Boxes } from "lucide-react";

import { ModelRowActions } from "@/components/admin/model-row-actions";
import { AdminModelsRefreshButton } from "@/components/admin/models-refresh-button";
import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import type { AdminModelsListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isAdminModelsListResponse(value: unknown): value is AdminModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatUsdPerM(value: string | null | undefined) {
  if (!value) return "—";
  return `$${value}`;
}

async function getMe() {
  const res = await fetch(buildBackendUrl("/auth/me"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!json || typeof json !== "object") return null;
  const role = (json as { role?: unknown }).role;
  const email = (json as { email?: unknown }).email;
  if (typeof role !== "string" || typeof email !== "string") return null;
  return { role, email };
}

async function getModels() {
  const res = await fetch(buildBackendUrl("/admin/models"), {
    cache: "force-cache",
    next: { tags: ["models:admin-config", "models:user"] },
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelsListResponse(json)) return null;
  return json.items;
}

export default async function AdminModelsPage() {
  const locale = await getRequestLocale();
  const me = await getMe();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const items = isAdmin ? (await getModels()) ?? [] : [];
  const current = t(locale, "admin.currentUser", { email: me?.email ?? "unknown" });

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.modelConfig")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(locale, "admin.models.subtitle", { current })}
          </p>
        </div>
        {isAdmin ? <AdminModelsRefreshButton /> : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Boxes className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.models.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>{t(locale, "admin.models.card.title")}</CardTitle>
            <CardDescription>{t(locale, "admin.models.card.desc")}</CardDescription>
          </CardHeader>
          <CardContent>
            {items.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
                {t(locale, "admin.models.empty")}
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t(locale, "models.table.model")}</TableHead>
                    <TableHead>{t(locale, "keys.table.status")}</TableHead>
                    <TableHead>{t(locale, "models.table.input")}</TableHead>
                    <TableHead>{t(locale, "models.table.output")}</TableHead>
                    <TableHead>{t(locale, "admin.models.table.sources")}</TableHead>
                    <TableHead className="w-12 text-right">{t(locale, "keys.table.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((m) => (
                    <TableRow key={m.model}>
                      <TableCell>
                        <CopyableModelId value={m.model} />
                      </TableCell>
                      <TableCell>
                        {!m.available ? (
                          <Badge variant="outline">{t(locale, "admin.models.badge.missing")}</Badge>
                        ) : m.enabled ? (
                          <Badge variant="success">{t(locale, "admin.models.badge.enabled")}</Badge>
                        ) : (
                          <Badge variant="destructive">{t(locale, "admin.models.badge.disabled")}</Badge>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatUsdPerM(m.inputUsdPerM)}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatUsdPerM(m.outputUsdPerM)}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {m.sources}
                      </TableCell>
                      <TableCell className="p-2 text-right">
                        <ModelRowActions model={m} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
