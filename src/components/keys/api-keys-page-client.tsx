"use client";

import * as React from "react";
import { Copy, Info, KeyRound } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyCreateResponse, ApiKeyItem, ApiKeyUpdateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
import { CreateKeyDialog } from "@/components/keys/create-key-dialog";
import { KeysTable } from "@/components/keys/keys-table";
import { Card, CardContent } from "@/components/ui/card";
import { useI18n } from "@/components/i18n/i18n-provider";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { PRIMARY_CTA_CLASSNAME } from "@/lib/ui-styles";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

interface ApiKeysPageClientProps {
  initialItems: ApiKeyItem[];
  publicApiBaseUrl?: string | null;
}

export function ApiKeysPageClient({ initialItems, publicApiBaseUrl }: ApiKeysPageClientProps) {
  const [items, setItems] = React.useState<ApiKeyItem[]>(initialItems);
  const [fullKeysById, setFullKeysById] = React.useState<Record<string, string>>({});
  const { t } = useI18n();

  function onCreated(res: ApiKeyCreateResponse) {
    setItems((prev) => [res.item, ...prev]);
    setFullKeysById((prev) => ({ ...prev, [res.item.id]: res.key }));
  }

  async function patchKey(id: string, body: Record<string, unknown>) {
    const res = await fetch(`/api/keys/${id}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body)
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const message =
        json && typeof json === "object" && "message" in json
          ? String((json as { message?: unknown }).message ?? t("keys.toast.updateFailed"))
          : t("keys.toast.updateFailed");
      throw new Error(message);
    }
    if (!json || typeof json !== "object" || !("item" in json)) return null;
    const item = (json as ApiKeyUpdateResponse).item;
    if (!item) return;
    setItems((prev) => prev.map((k) => (k.id === id ? item : k)));
    return item;
  }

  async function setRevoked(id: string, revoked: boolean) {
    await patchKey(id, { revoked });
  }

  async function renameKey(id: string, name: string) {
    await patchKey(id, { name });
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

  async function copyApiBaseUrl() {
    if (!publicApiBaseUrl) return;
    try {
      await navigator.clipboard.writeText(publicApiBaseUrl);
      toast.success(t("keys.dialog.copySuccess"));
    } catch {
      toast.error(t("keys.dialog.copyFailed"));
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("keys.title")}
        description={
          <TooltipProvider delayDuration={150}>
            <div className="space-y-2">
              <div>{t("keys.subtitle")}</div>
              {publicApiBaseUrl ? (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-muted-foreground">{t("keys.baseUrl.label")}</span>
                  <span className="rounded-lg border border-border bg-muted/20 px-2 py-1 font-mono text-xs tabular-nums text-foreground">
                    {publicApiBaseUrl}
                  </span>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        type="button"
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8 rounded-xl"
                        onClick={() => void copyApiBaseUrl()}
                        aria-label={t("keys.baseUrl.copy")}
                      >
                        <Copy className="h-4 w-4" />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>{t("keys.baseUrl.copy")}</TooltipContent>
                  </Tooltip>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        type="button"
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8 rounded-xl"
                        aria-label={t("keys.baseUrl.moreInfo")}
                      >
                        <Info className="h-4 w-4" />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent className="max-w-[360px]">{t("keys.baseUrl.help")}</TooltipContent>
                  </Tooltip>
                </div>
              ) : null}
            </div>
          </TooltipProvider>
        }
        actions={
          <CreateKeyDialog
            onCreated={onCreated}
            onRenamed={(item) => {
              setItems((prev) => prev.map((k) => (k.id === item.id ? item : k)));
            }}
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
            onRename={async (id, name) => {
              await renameKey(id, name);
              toast.success(t("keys.toast.renamed"));
            }}
            emptyState={
              <div className="p-6">
                <EmptyState
                  icon={<KeyRound className="h-6 w-6 text-muted-foreground uai-float-sm" />}
                  title={t("keys.empty.title")}
                  description={t("keys.empty.desc")}
                  action={
                    <CreateKeyDialog
                      onCreated={onCreated}
                      onRenamed={(item) => {
                        setItems((prev) => prev.map((k) => (k.id === item.id ? item : k)));
                      }}
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
