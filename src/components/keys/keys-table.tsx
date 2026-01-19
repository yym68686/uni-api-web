"use client";

import * as React from "react";
import { Copy, Loader2, MoreHorizontal, PencilLine, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyRevealResponse } from "@/lib/types";
import type { ApiKeyItem } from "@/lib/types";
import { formatUsd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { useI18n } from "@/components/i18n/i18n-provider";

function formatSpendUsd(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "$0";
  return formatUsd(value);
}

interface KeysTableProps {
  items: ApiKeyItem[];
  fullKeysById?: Record<string, string>;
  onToggleRevoked: (id: string, revoked: boolean) => Promise<void> | void;
  onDelete: (id: string) => Promise<void> | void;
  onRename: (id: string, name: string) => Promise<void> | void;
  emptyState?: React.ReactNode;
  className?: string;
}

export function KeysTable({
  items,
  fullKeysById,
  onToggleRevoked,
  onDelete,
  onRename,
  emptyState,
  className
}: KeysTableProps) {
  const { locale, t } = useI18n();
  const [revokingId, setRevokingId] = React.useState<string | null>(null);
  const [deletingId, setDeletingId] = React.useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = React.useState<ApiKeyItem | null>(null);
  const [copyingId, setCopyingId] = React.useState<string | null>(null);
  const [renameTarget, setRenameTarget] = React.useState<ApiKeyItem | null>(null);
  const [renameValue, setRenameValue] = React.useState("");
  const [renamingId, setRenamingId] = React.useState<string | null>(null);

  const dateTimeFormatter = React.useMemo(() => {
    return new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit"
    });
  }, [locale]);

  function formatDateTime(value?: string) {
    if (!value) return "—";
    const dt = new Date(value);
    if (Number.isNaN(dt.getTime())) return "—";
    return dateTimeFormatter.format(dt);
  }

  async function toggleRevoked(id: string, revoked: boolean) {
    setRevokingId(id);
    try {
      await onToggleRevoked(id, revoked);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
    } finally {
      setRevokingId(null);
    }
  }

  async function remove(id: string) {
    setDeletingId(id);
    try {
      await onDelete(id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.deleteFailed"));
    } finally {
      setDeletingId(null);
    }
  }

  async function rename(id: string, name: string) {
    const trimmed = name.trim();
    if (trimmed.length < 2) {
      toast.error(t("validation.minChars", { min: 2 }));
      return;
    }
    if (trimmed.length > 64) {
      toast.error(t("validation.maxChars", { max: 64 }));
      return;
    }
    if (renamingId) return;
    setRenamingId(id);
    try {
      await onRename(id, trimmed);
      setRenameTarget(null);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
    } finally {
      setRenamingId(null);
    }
  }

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t("keys.dialog.copySuccess"));
    } catch {
      toast.error(t("keys.dialog.copyFailed"));
    }
  }

  function isRevealResponse(value: unknown): value is ApiKeyRevealResponse {
    if (!value || typeof value !== "object") return false;
    const v = value as { key?: unknown };
    return typeof v.key === "string" && v.key.length > 10;
  }

  async function copyFullKey(id: string) {
    const full = fullKeysById?.[id];
    if (full) {
      await copy(full);
      return;
    }

    setCopyingId(id);
    try {
      const res = await fetch(`/api/keys/${encodeURIComponent(id)}/reveal`, { cache: "no-store" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("common.copyFailed"))
            : t("common.copyFailed");
        throw new Error(message);
      }
      if (!isRevealResponse(json)) {
        throw new Error(t("keys.dialog.copyFailed"));
      }
      await copy(json.key);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.dialog.copyFailed"));
    } finally {
      setCopyingId(null);
    }
  }

  return (
    <TooltipProvider delayDuration={150}>
      <div className={cn("space-y-4", className)}>
        {items.length === 0 && emptyState ? (
          emptyState
        ) : (
          <Table variant="card">
            <TableHeader>
              <TableRow>
                <TableHead>{t("keys.table.name")}</TableHead>
                <TableHead>{t("keys.table.key")}</TableHead>
                <TableHead>{t("keys.table.lastUsed")}</TableHead>
                <TableHead>{t("keys.table.totalSpend")}</TableHead>
                <TableHead>{t("keys.table.createdAt")}</TableHead>
                <TableHead>{t("keys.table.status")}</TableHead>
                <TableHead className="w-12 text-right"> </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((k) => {
                const revoked = Boolean(k.revokedAt);
                const isCopying = copyingId === k.id;
                return (
                  <TableRow key={k.id}>
                    <TableCell className="font-medium">{k.name}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      <div className="flex items-center gap-2">
                        <span className="truncate tracking-wide">{k.prefix}</span>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              size="icon"
                              variant="ghost"
                              className="h-8 w-8 rounded-xl"
                              disabled={isCopying}
                              onClick={() => {
                                void copyFullKey(k.id);
                              }}
                            >
                              {isCopying ? (
                                <Loader2 className="h-4 w-4 animate-spin" />
                              ) : (
                                <Copy className="h-4 w-4" />
                              )}
                              <span className="sr-only">{t("keys.table.copy")}</span>
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>{t("keys.table.copyFullKey")}</TooltipContent>
                        </Tooltip>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono tabular-nums">
                      {formatDateTime(k.lastUsedAt)}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono tabular-nums">
                      {formatSpendUsd(k.spendUsd)}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono tabular-nums">
                      {formatDateTime(k.createdAt)}
                    </TableCell>
                    <TableCell>
                      {revoked ? (
                        <Badge variant="destructive">{t("keys.table.revoked")}</Badge>
                      ) : (
                        <Badge variant="success">{t("keys.table.active")}</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8 rounded-xl"
                            aria-label="Actions"
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            disabled={deletingId === k.id || revokingId === k.id || isCopying}
                            onClick={() => {
                              setRenameTarget(k);
                              setRenameValue(k.name);
                            }}
                          >
                            <PencilLine className="mr-2 h-4 w-4" />
                            {t("keys.table.rename")}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={isCopying}
                            onClick={() => {
                              void copyFullKey(k.id);
                            }}
                          >
                            <Copy className="mr-2 h-4 w-4" />
                            {t("keys.table.copyFullKey")}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={revokingId === k.id || deletingId === k.id || isCopying}
                            onClick={() => {
                              void toggleRevoked(k.id, !revoked);
                            }}
                          >
                            <RotateCcw className="mr-2 h-4 w-4" />
                            {revokingId === k.id
                              ? t("keys.table.updating")
                              : revoked
                                ? t("keys.table.restore")
                                : t("keys.table.revoke")}
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            disabled={deletingId === k.id}
                            onClick={() => {
                              setDeleteTarget(k);
                            }}
                            className="text-destructive focus:text-destructive"
                          >
                            <Trash2 className="mr-2 h-4 w-4" />
                            {deletingId === k.id ? t("keys.table.deleting") : t("keys.table.delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        <Dialog
          open={renameTarget != null}
          onOpenChange={(open) => {
            if (!open) setRenameTarget(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("keys.rename.title")}</DialogTitle>
              <DialogDescription>{t("keys.rename.desc")}</DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="rename-name">{t("keys.dialog.name")}</Label>
              <Input
                id="rename-name"
                value={renameValue}
                autoComplete="off"
                onChange={(e) => setRenameValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key !== "Enter") return;
                  if (!renameTarget) return;
                  e.preventDefault();
                  void rename(renameTarget.id, renameValue);
                }}
              />
              <p className="text-xs text-muted-foreground">{t("keys.dialog.nameHelp")}</p>
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setRenameTarget(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                type="button"
                disabled={!renameTarget || renamingId === renameTarget.id}
                onClick={() => {
                  if (!renameTarget) return;
                  void rename(renameTarget.id, renameValue);
                }}
              >
                {renameTarget && renamingId === renameTarget.id ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    {t("common.saving")}
                  </>
                ) : (
                  <>
                    <PencilLine className="h-4 w-4" />
                    {t("common.save")}
                  </>
                )}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        <Dialog
          open={deleteTarget != null}
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("keys.table.deleteDialogTitle")}</DialogTitle>
              <DialogDescription>{t("keys.table.deleteDialogDesc")}</DialogDescription>
            </DialogHeader>
            {deleteTarget ? (
              <div className="rounded-xl border border-border bg-muted/20 p-4">
                <div className="text-sm font-medium text-foreground">{deleteTarget.name}</div>
                <div className="mt-1 text-xs font-mono text-muted-foreground">{deleteTarget.prefix}</div>
              </div>
            ) : null}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setDeleteTarget(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={deleteTarget ? deletingId === deleteTarget.id : true}
                onClick={() => {
                  if (!deleteTarget) return;
                  void remove(deleteTarget.id).then(() => setDeleteTarget(null));
                }}
              >
                {deleteTarget && deletingId === deleteTarget.id ? t("keys.table.deleting") : t("keys.table.delete")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </TooltipProvider>
  );
}
