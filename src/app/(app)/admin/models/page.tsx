import { Boxes, RefreshCw } from "lucide-react";

import { ModelRowActions } from "@/components/admin/model-row-actions";
import { CopyableModelId } from "@/components/models/copyable-model-id";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
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
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelsListResponse(json)) return null;
  return json.items;
}

export default async function AdminModelsPage() {
  const me = await getMe();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const items = isAdmin ? (await getModels()) ?? [] : [];

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Models</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            从各渠道的 `/v1/models` 自动聚合去重，并设置启用与价格（当前：{me?.email ?? "unknown"}）。
          </p>
        </div>
        {isAdmin ? (
          <form action="">
            <Button type="submit" variant="outline" className="rounded-xl bg-transparent">
              <RefreshCw className="h-4 w-4" />
              Refresh
            </Button>
          </form>
        ) : null}
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Boxes className="h-5 w-5 text-muted-foreground" />
              Forbidden
            </CardTitle>
            <CardDescription>你不是管理员，无法管理模型。</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Available models</CardTitle>
            <CardDescription>关闭模型后，网关会拒绝对应的请求。</CardDescription>
          </CardHeader>
          <CardContent>
            {items.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
                未发现模型（请先配置 Channels）
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Model</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Input ($/M tokens)</TableHead>
                    <TableHead>Output ($/M tokens)</TableHead>
                    <TableHead>Sources</TableHead>
                    <TableHead className="w-12 text-right">Actions</TableHead>
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
                          <Badge variant="outline">Missing</Badge>
                        ) : m.enabled ? (
                          <Badge variant="success">Enabled</Badge>
                        ) : (
                          <Badge variant="destructive">Disabled</Badge>
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
