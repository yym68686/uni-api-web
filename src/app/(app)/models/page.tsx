import { Boxes } from "lucide-react";

import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import type { ModelsListResponse } from "@/lib/types";

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
  const items = (await getModels()) ?? [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Models</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          可用模型会根据你的用户组（Group）与管理员配置的渠道权限自动变化。
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Boxes className="h-5 w-5 text-muted-foreground" />
            Available models
          </CardTitle>
          <CardDescription>价格按 $/M tokens 计费（展示为 $X）。</CardDescription>
        </CardHeader>
        <CardContent>
          {items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              暂无可用模型（请联系管理员配置 Channels 与模型开关）
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Model</TableHead>
                  <TableHead>Input ($/M tokens)</TableHead>
                  <TableHead>Output ($/M tokens)</TableHead>
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
