import { Boxes } from "lucide-react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { ModelsListResponse } from "@/lib/types";

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
  const res = await fetch(buildBackendUrl("/console/models"), {
    cache: "force-cache",
    next: { tags: ["models:user"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isModelsListResponse(json)) return null;
  return json.items;
}

interface ModelsContentProps {
  locale: Locale;
}

export async function ModelsContent({ locale }: ModelsContentProps) {
  const items = (await getModels()) ?? [];

  return (
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
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl border border-border bg-background/50">
              <Boxes className="h-6 w-6 text-muted-foreground uai-float-sm" />
            </div>
            <div className="mt-3">{t(locale, "models.empty")}</div>
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
                <TableRow key={m.model}>
                  <TableCell>
                    <CopyableModelId value={m.model} />
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {formatUsdPerM(m.inputUsdPerM)}
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {formatUsdPerM(m.outputUsdPerM)}
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
