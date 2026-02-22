"use client";

import * as React from "react";
import { Copy, KeyRound, Loader2 } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyCreateResponse, ApiKeyItem, ApiKeyUpdateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useI18n } from "@/components/i18n/i18n-provider";

interface CreateKeyDialogProps {
  onCreated: (res: ApiKeyCreateResponse) => void;
  onRenamed?: (item: ApiKeyItem) => void;
  triggerLabel?: string;
  triggerClassName?: string;
}

interface ApiErrorBody {
  message?: string;
  issues?: Array<{ message?: string }>;
}

function extractApiErrorMessage(body: unknown): string | null {
  if (!body || typeof body !== "object") return null;
  const obj = body as ApiErrorBody;
  const issueMessage = obj.issues?.[0]?.message;
  if (typeof issueMessage === "string" && issueMessage.length > 0) return issueMessage;
  if (typeof obj.message === "string" && obj.message.length > 0) return obj.message;
  return null;
}

function generateDefaultName() {
  const date = new Date().toISOString().slice(0, 10).replaceAll("-", "");
  const uuid =
    typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Math.random()}`;
  const suffix = uuid.replaceAll("-", "").slice(0, 8);
  return `key-${date}-${suffix}`;
}

function isApiKeyUpdateResponse(value: unknown): value is ApiKeyUpdateResponse {
  if (!value || typeof value !== "object") return false;
  return "item" in value;
}

export function CreateKeyDialog({ onCreated, onRenamed, triggerLabel, triggerClassName }: CreateKeyDialogProps) {
  const { t } = useI18n();
  const [open, setOpen] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [createdKey, setCreatedKey] = React.useState<string | null>(null);
  const [createdItem, setCreatedItem] = React.useState<ApiKeyItem | null>(null);
  const [name, setName] = React.useState("");
  const [savingName, setSavingName] = React.useState(false);

  const triggerText = triggerLabel ?? t("keys.create");

  function reset() {
    setCreatedKey(null);
    setCreatedItem(null);
    setName("");
    setSavingName(false);
    setCreating(false);
  }

  async function copyKey(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t("keys.dialog.copySuccess"));
    } catch {
      toast.error(t("keys.dialog.copyFailed"));
    }
  }

  async function createNow() {
    if (creating) return;
    setCreating(true);
    try {
      const res = await fetch("/api/keys", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name: generateDefaultName() })
      });
      if (!res.ok) {
        let message = t("keys.toast.updateFailed");
        try {
          const body: unknown = await res.json();
          message = extractApiErrorMessage(body) ?? message;
        } catch {
          // ignore
        }
        throw new Error(message);
      }
      const json: ApiKeyCreateResponse = (await res.json()) as ApiKeyCreateResponse;
      setCreatedKey(json.key);
      setCreatedItem(json.item);
      setName(json.item.name);
      onCreated(json);
      setOpen(true);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
    } finally {
      setCreating(false);
    }
  }

  async function saveRename() {
    if (!createdItem) return true;
    const trimmed = name.trim();
    if (trimmed === createdItem.name) return true;
    if (trimmed.length < 2) {
      toast.error(t("validation.minChars", { min: 2 }));
      return false;
    }
    if (trimmed.length > 64) {
      toast.error(t("validation.maxChars", { max: 64 }));
      return false;
    }
    setSavingName(true);
    try {
      const res = await fetch(`/api/keys/${encodeURIComponent(createdItem.id)}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name: trimmed })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message = extractApiErrorMessage(json) ?? t("keys.toast.updateFailed");
        throw new Error(message);
      }
      if (!isApiKeyUpdateResponse(json)) throw new Error(t("common.unexpectedError"));
      const next = json.item;
      setCreatedItem(next);
      onRenamed?.(next);
      toast.success(t("keys.toast.renamed"));
      return true;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
      return false;
    } finally {
      setSavingName(false);
    }
  }

  async function saveAndClose() {
    if (savingName) return;
    const ok = await saveRename();
    if (!ok) return;
    setOpen(false);
    reset();
  }

  return (
    <>
      <Button
        className={cn("rounded-xl", triggerClassName)}
        disabled={creating}
        onClick={() => void createNow()}
      >
        {creating ? (
          <span className="inline-flex animate-spin">
            <Loader2 className="h-4 w-4" />
          </span>
        ) : (
          <KeyRound className="h-4 w-4" />
        )}
        {creating ? t("keys.dialog.creating") : triggerText}
      </Button>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) reset();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("keys.dialog.title")}</DialogTitle>
            <DialogDescription>{t("keys.dialog.desc")}</DialogDescription>
          </DialogHeader>

          {createdKey ? (
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="key-name">{t("keys.dialog.name")}</Label>
                <Input
                  id="key-name"
                  value={name}
                  autoComplete="off"
                  onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key !== "Enter") return;
                    e.preventDefault();
                    void saveRename();
                  }}
                />
                <p className="text-xs text-muted-foreground">{t("keys.dialog.nameHelp")}</p>
              </div>

              <div className="rounded-xl border border-border bg-muted/30 p-3">
                <div className="text-xs text-muted-foreground">{t("keys.dialog.yourKey")}</div>
                <div className="mt-2 break-all font-mono tabular-nums tracking-wide text-sm">{createdKey}</div>
              </div>

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => void copyKey(createdKey)}>
                  <Copy className="h-4 w-4" />
                  {t("keys.dialog.copy")}
                </Button>
                <Button
                  type="button"
                  disabled={savingName}
                  onClick={() => {
                    void saveAndClose();
                  }}
                >
                  {savingName ? (
                    <span className="inline-flex animate-spin">
                      <Loader2 className="h-4 w-4" />
                    </span>
                  ) : null}
                  {savingName ? t("common.saving") : t("keys.dialog.done")}
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <span className="inline-flex animate-spin">
                <Loader2 className="h-4 w-4" />
              </span>
              {t("common.working")}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
