"use client";

import * as React from "react";
import { KeyRound } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyCreateResponse, ApiKeyItem } from "@/lib/types";
import { cn } from "@/lib/utils";
import { CreateKeyDialog } from "@/components/keys/create-key-dialog";
import { KeysTable } from "@/components/keys/keys-table";
import { Card, CardContent } from "@/components/ui/card";
import { useI18n } from "@/components/i18n/i18n-provider";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { PRIMARY_CTA_CLASSNAME } from "@/lib/ui-styles";

interface ApiKeysPageClientProps {
  initialItems: ApiKeyItem[];
}

export function ApiKeysPageClient({ initialItems }: ApiKeysPageClientProps) {
  const [items, setItems] = React.useState<ApiKeyItem[]>(initialItems);
  const [fullKeysById, setFullKeysById] = React.useState<Record<string, string>>({});
  const { t } = useI18n();

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
          ? String((json as { message?: unknown }).message ?? t("keys.toast.updateFailed"))
          : t("keys.toast.updateFailed");
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
          ? String((json as { message?: unknown }).message ?? t("keys.toast.deleteFailed"))
          : t("keys.toast.deleteFailed");
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
      toast.success(revoked ? t("keys.toast.revoked") : t("keys.toast.restored"));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
    }
  }

  async function onDelete(id: string) {
    try {
      await deleteKey(id);
      toast.success(t("keys.toast.deleted"));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.deleteFailed"));
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("keys.title")}
        description={t("keys.subtitle")}
        actions={
          <CreateKeyDialog
            onCreated={onCreated}
            triggerLabel={t("keys.create")}
            triggerClassName={cn("uai-border-beam", PRIMARY_CTA_CLASSNAME)}
          />
        }
      />

      <Card>
        <CardContent className="p-0">
          <KeysTable
            items={items}
            fullKeysById={fullKeysById}
            onToggleRevoked={onToggleRevoked}
            onDelete={onDelete}
            emptyState={
              <div className="p-6">
                <EmptyState
                  icon={<KeyRound className="h-6 w-6 text-muted-foreground uai-float-sm" />}
                  title={t("keys.empty.title")}
                  description={t("keys.empty.desc")}
                  action={
                    <CreateKeyDialog
                      onCreated={onCreated}
                      triggerLabel={t("keys.create")}
                      triggerClassName={cn("uai-border-beam", PRIMARY_CTA_CLASSNAME)}
                    />
                  }
                />
              </div>
            }
          />
        </CardContent>
      </Card>
    </div>
  );
}
