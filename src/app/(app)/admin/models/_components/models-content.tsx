import { ModelRowActions } from "@/components/admin/model-row-actions";
import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { AdminModelsListResponse } from "@/lib/types";

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

async function getModels() {
  const res = await fetch(buildBackendUrl("/admin/models"), {
    cache: "force-cache",
    next: { tags: ["models:admin-config", "models:user"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelsListResponse(json)) return null;
  return json.items;
}

interface AdminModelsContentProps {
  locale: Locale;
}

export async function AdminModelsContent({ locale }: AdminModelsContentProps) {
  const items = (await getModels()) ?? [];

  return (
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
                  <TableCell className="font-mono text-xs text-muted-foreground">{m.sources}</TableCell>
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
  );
}
