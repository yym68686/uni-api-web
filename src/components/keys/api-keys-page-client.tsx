"use client";

import * as React from "react";
import { KeyRound } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyCreateResponse, ApiKeyItem } from "@/lib/types";
import { cn } from "@/lib/utils";
import { CreateKeyDialog } from "@/components/keys/create-key-dialog";
import { KeysTable } from "@/components/keys/keys-table";
import { Card, CardContent } from "@/components/ui/card";

interface ApiKeysPageClientProps {
  initialItems: ApiKeyItem[];
}

const createButtonGlow =
  "shadow-[0_0_0_1px_oklch(var(--primary)/0.25),0_12px_30px_oklch(var(--primary)/0.22)] hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_16px_40px_oklch(var(--primary)/0.28)]";

export function ApiKeysPageClient({ initialItems }: ApiKeysPageClientProps) {
  const [items, setItems] = React.useState<ApiKeyItem[]>(initialItems);
  const [fullKeysById, setFullKeysById] = React.useState<Record<string, string>>({});

  function onCreated(res: ApiKeyCreateResponse) {
    setItems((prev) => [res.item, ...prev]);
    setFullKeysById((prev) => ({ ...prev, [res.item.id]: res.key }));
  }

  async function setRevoked(id: string, revoked: boolean) {
    const res = await fetch(`/api/keys/${id}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ revoked })
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const message =
        json && typeof json === "object" && "message" in json
          ? String((json as { message?: unknown }).message ?? "更新失败")
          : "更新失败";
      throw new Error(message);
    }
    if (!json || typeof json !== "object" || !("item" in json)) return;
    const item = (json as { item?: ApiKeyItem }).item;
    if (!item) return;
    setItems((prev) => prev.map((k) => (k.id === id ? item : k)));
  }

  async function deleteKey(id: string) {
    const res = await fetch(`/api/keys/${id}`, { method: "DELETE" });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const message =
        json && typeof json === "object" && "message" in json
          ? String((json as { message?: unknown }).message ?? "删除失败")
          : "删除失败";
      throw new Error(message);
    }
    setItems((prev) => prev.filter((k) => k.id !== id));
    setFullKeysById((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }

  async function onToggleRevoked(id: string, revoked: boolean) {
    try {
      await setRevoked(id, revoked);
      toast.success(revoked ? "已撤销 Key" : "已恢复 Key");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
    }
  }

  async function onDelete(id: string) {
    try {
      await deleteKey(id);
      toast.success("已删除 Key");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">API Keys</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            创建与管理访问凭证（列表仅显示掩码；完整 Key 只展示一次）。
          </p>
        </div>

        <CreateKeyDialog
          onCreated={onCreated}
          triggerLabel="Create New Key"
          triggerClassName={cn("rounded-xl", createButtonGlow)}
        />
      </div>

      <Card>
        <CardContent className="p-0">
          <KeysTable
            items={items}
            fullKeysById={fullKeysById}
            onToggleRevoked={onToggleRevoked}
            onDelete={onDelete}
            emptyState={
              <div className="p-6">
                <div className="rounded-xl border border-dashed border-border bg-muted/10 p-10 text-center">
                  <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl border border-border bg-background/50">
                    <KeyRound className="h-6 w-6 text-muted-foreground" />
                  </div>
                  <div className="mt-4 text-base font-semibold text-foreground">
                    No API keys yet
                  </div>
                  <div className="mt-1 text-sm text-muted-foreground">
                    创建一个新的 Key 来开始调用 API。
                  </div>
                  <div className="mt-5 flex justify-center">
                    <CreateKeyDialog
                      onCreated={onCreated}
                      triggerLabel="Create New Key"
                      triggerClassName={cn("rounded-xl", createButtonGlow)}
                    />
                  </div>
                </div>
              </div>
            }
          />
        </CardContent>
      </Card>
    </div>
  );
}
