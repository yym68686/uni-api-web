import { Boxes } from "lucide-react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import type { ModelsListResponse } from "@/lib/types";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";

export const dynamic = "force-dynamic";

function isModelsListResponse(value: unknown): value is ModelsListResponse {
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
  const res = await fetch(buildBackendUrl("/models"), {
    cache: "force-cache",
    next: { tags: ["models:user"] },
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isModelsListResponse(json)) return null;
  return json.items;
}

export default async function ModelsPage() {
  const locale = await getRequestLocale();
  const items = (await getModels()) ?? [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "models.title")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(locale, "models.subtitle")}
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Boxes className="h-5 w-5 text-muted-foreground" />
            {t(locale, "models.card.title")}
          </CardTitle>
          <CardDescription>{t(locale, "models.card.desc")}</CardDescription>
        </CardHeader>
        <CardContent>
          {items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              {t(locale, "models.empty")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(locale, "models.table.model")}</TableHead>
                  <TableHead>{t(locale, "models.table.input")}</TableHead>
                  <TableHead>{t(locale, "models.table.output")}</TableHead>
                </TableRow>
              </TableHeader>
                <TableBody>
                  {items.map((m) => (
                    <TableRow key={m.model} className="hover:bg-muted/50">
                      <TableCell>
                        <CopyableModelId value={m.model} />
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatUsdPerM(m.inputUsdPerM)}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                      {formatUsdPerM(m.outputUsdPerM)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
