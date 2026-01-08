"use client";

import * as React from "react";
import { Copy, MoreHorizontal, Trash2 } from "lucide-react";
import { toast } from "sonner";

import type { ApiKeyItem } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
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

interface KeysTableProps {
  items: ApiKeyItem[];
  fullKeysById?: Record<string, string>;
  onRevoke: (id: string) => Promise<void> | void;
  emptyState?: React.ReactNode;
  className?: string;
}

export function KeysTable({ items, fullKeysById, onRevoke, emptyState, className }: KeysTableProps) {
  const [revokingId, setRevokingId] = React.useState<string | null>(null);

  async function revoke(id: string) {
    setRevokingId(id);
    try {
      await onRevoke(id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "撤销失败");
    } finally {
      setRevokingId(null);
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

  async function copyFullKey(id: string) {
    const full = fullKeysById?.[id];
    if (!full) {
      toast.error("完整 Key 仅在创建时展示一次，无法再次复制");
      return;
    }
    await copy(full);
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
                <TableHead>Created At</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="w-12 text-right"> </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((k) => {
                const revoked = Boolean(k.revokedAt);
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
                              onClick={() => {
                                void copyFullKey(k.id);
                              }}
                            >
                              <Copy className="h-4 w-4" />
                              <span className="sr-only">Copy</span>
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>
                            {fullKeysById?.[k.id] ? "复制完整 Key" : "完整 Key 不可再次获取"}
                          </TooltipContent>
                        </Tooltip>
                      </div>
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
                            onClick={() => {
                              void copyFullKey(k.id);
                            }}
                          >
                            <Copy className="mr-2 h-4 w-4" />
                            Copy full key
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => {
                              void copy(k.prefix);
                            }}
                          >
                            <Copy className="mr-2 h-4 w-4" />
                            Copy masked key
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => {
                              void copy(k.id);
                            }}
                          >
                            <Copy className="mr-2 h-4 w-4" />
                            Copy ID
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={revoked || revokingId === k.id}
                            onClick={() => {
                              void revoke(k.id);
                            }}
                            className="text-destructive focus:text-destructive"
                          >
                            <Trash2 className="mr-2 h-4 w-4" />
                            {revokingId === k.id ? "Revoking…" : "Revoke"}
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
      </div>
    </TooltipProvider>
  );
}
