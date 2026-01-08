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

  async function revoke(id: string) {
    const res = await fetch(`/api/keys/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(await res.text());
    setItems((prev) =>
      prev.map((k) => (k.id === id ? { ...k, revokedAt: new Date().toISOString() } : k))
    );
  }

  async function onRevoke(id: string) {
    try {
      await revoke(id);
      toast.success("已撤销 Key");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "撤销失败");
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
            onRevoke={onRevoke}
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
