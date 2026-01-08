"use client";

import * as React from "react";
import { Copy, Loader2, MoreHorizontal, RotateCcw, Trash2 } from "lucide-react";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

function formatDateTime(value?: string) {
  if (!value) return "—";
  const dt = new Date(value);
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(dt);
}

function formatSpendUsd(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "$0";
  return formatUsd(value);
}

interface KeysTableProps {
  items: ApiKeyItem[];
  fullKeysById?: Record<string, string>;
  onToggleRevoked: (id: string, revoked: boolean) => Promise<void> | void;
  onDelete: (id: string) => Promise<void> | void;
  emptyState?: React.ReactNode;
  className?: string;
}

export function KeysTable({
  items,
  fullKeysById,
  onToggleRevoked,
  onDelete,
  emptyState,
  className
}: KeysTableProps) {
  const [revokingId, setRevokingId] = React.useState<string | null>(null);
  const [deletingId, setDeletingId] = React.useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = React.useState<ApiKeyItem | null>(null);
  const [copyingId, setCopyingId] = React.useState<string | null>(null);

  async function toggleRevoked(id: string, revoked: boolean) {
    setRevokingId(id);
    try {
      await onToggleRevoked(id, revoked);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
    } finally {
      setRevokingId(null);
    }
  }

  async function remove(id: string) {
    setDeletingId(id);
    try {
      await onDelete(id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
    } finally {
      setDeletingId(null);
    }
  }

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      toast.success("已复制");
    } catch {
      toast.error("复制失败");
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
            ? String((json as { message?: unknown }).message ?? "复制失败")
            : "复制失败";
        throw new Error(message);
      }
      if (!isRevealResponse(json)) {
        throw new Error("无法获取完整 Key（可能是旧 Key，请重新创建）");
      }
      await copy(json.key);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "复制失败");
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
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Last Used</TableHead>
                <TableHead>Total Spend</TableHead>
                <TableHead>Created At</TableHead>
                <TableHead>Status</TableHead>
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
                    <TableCell className="font-mono text-xs">
                      <div className="flex items-center gap-2">
                        <span className="truncate">{k.prefix}</span>
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
                              <span className="sr-only">Copy</span>
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>复制完整 Key</TooltipContent>
                        </Tooltip>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono">
                      {formatDateTime(k.lastUsedAt)}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono">
                      {formatSpendUsd(k.spendUsd)}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground font-mono">
                      {formatDateTime(k.createdAt)}
                    </TableCell>
                    <TableCell>
                      {revoked ? (
                        <Badge variant="destructive">Revoked</Badge>
                      ) : (
                        <Badge variant="success">Active</Badge>
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
                            disabled={isCopying}
                            onClick={() => {
                              void copyFullKey(k.id);
                            }}
                          >
                            <Copy className="mr-2 h-4 w-4" />
                            Copy full key
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={revokingId === k.id || deletingId === k.id || isCopying}
                            onClick={() => {
                              void toggleRevoked(k.id, !revoked);
                            }}
                          >
                            <RotateCcw className="mr-2 h-4 w-4" />
                            {revokingId === k.id ? "Updating…" : revoked ? "Restore" : "Revoke"}
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
                            {deletingId === k.id ? "Deleting…" : "Delete"}
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
          open={deleteTarget != null}
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete API key?</DialogTitle>
              <DialogDescription>将永久删除该 Key，无法恢复。</DialogDescription>
            </DialogHeader>
            {deleteTarget ? (
              <div className="rounded-xl border border-border bg-muted/20 p-4">
                <div className="text-sm font-medium text-foreground">{deleteTarget.name}</div>
                <div className="mt-1 text-xs font-mono text-muted-foreground">{deleteTarget.prefix}</div>
              </div>
            ) : null}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setDeleteTarget(null)}>
                Cancel
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
                {deleteTarget && deletingId === deleteTarget.id ? "Deleting…" : "Delete"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </TooltipProvider>
  );
}
