"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, MoreVertical, Pencil, Shield, Trash2, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { LlmChannelDeleteResponse, LlmChannelItem, LlmChannelUpdateResponse } from "@/lib/types";
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

const schema = z.object({
  name: z.string().trim().min(2, "至少 2 个字符").max(64, "最多 64 个字符"),
  baseUrl: z
    .string()
    .trim()
    .min(8, "请输入 Base URL")
    .max(400, "最多 400 个字符")
    .refine((v) => {
      try {
        const u = new URL(v);
        return u.protocol === "http:" || u.protocol === "https:";
      } catch {
        return false;
      }
    }, "Base URL 不合法"),
  apiKey: z.string().trim().min(0).max(4096).optional(),
  allowGroups: z
    .array(
      z
        .string()
        .trim()
        .min(1)
        .max(64)
        .refine((v) => !v.includes("\n") && !v.includes("\r"), "分组包含非法字符")
    )
    .default([])
});

type FormValues = z.infer<typeof schema>;

interface ChannelRowActionsProps {
  channel: LlmChannelItem;
  className?: string;
}

function normalizeGroups(value: string[]) {
  const unique = new Set<string>();
  for (const raw of value) {
    const v = raw.trim();
    if (!v) continue;
    unique.add(v);
  }
  return Array.from(unique);
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function ChannelRowActions({ channel, className }: ChannelRowActionsProps) {
  const router = useRouter();
  const [editOpen, setEditOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [groupDraft, setGroupDraft] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  const form = useForm<FormValues>({
    defaultValues: {
      name: channel.name,
      baseUrl: channel.baseUrl,
      apiKey: "",
      allowGroups: channel.allowGroups ?? []
    },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (!editOpen) return;
    form.reset({
      name: channel.name,
      baseUrl: channel.baseUrl,
      apiKey: "",
      allowGroups: channel.allowGroups ?? []
    });
    setGroupDraft("");
  }, [channel.allowGroups, channel.baseUrl, channel.name, editOpen, form]);

  function addGroupsFromDraft() {
    const parts = groupDraft
      .split(/[,\n]/g)
      .map((p) => p.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    const current = form.getValues("allowGroups");
    form.setValue("allowGroups", normalizeGroups([...current, ...parts]), { shouldValidate: true });
    setGroupDraft("");
  }

  async function update(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? "表单校验失败");
        return;
      }

      const payload: Record<string, unknown> = {
        name: parsed.data.name,
        baseUrl: parsed.data.baseUrl,
        allowGroups: parsed.data.allowGroups
      };
      const apiKey = parsed.data.apiKey?.trim() ?? "";
      if (apiKey.length > 0) payload.apiKey = apiKey;

      const res = await fetch(`/api/admin/channels/${encodeURIComponent(channel.id)}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload)
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, "更新失败"));

      void (json as LlmChannelUpdateResponse);
      toast.success("渠道已更新");
      setEditOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function remove() {
    setSubmitting(true);
    try {
      const res = await fetch(`/api/admin/channels/${encodeURIComponent(channel.id)}`, {
        method: "DELETE"
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, "删除失败"));
      void (json as LlmChannelDeleteResponse);
      toast.success("渠道已删除");
      setDeleteOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
    } finally {
      setSubmitting(false);
    }
  }

  const allowGroups = form.watch("allowGroups");

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className={cn("h-9 w-9 rounded-lg", className)}
            aria-label="Channel actions"
          >
            <MoreVertical className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault();
              setEditOpen(true);
            }}
          >
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="text-destructive focus:text-destructive"
            onSelect={(e) => {
              e.preventDefault();
              setDeleteOpen(true);
            }}
          >
            <Trash2 className="mr-2 h-4 w-4" />
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-primary" />
              Edit channel
            </DialogTitle>
            <DialogDescription>可更新 Base URL、分组白名单，以及旋转 API key。</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(update)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`name-${channel.id}`}>Name</Label>
              <Input id={`name-${channel.id}`} {...form.register("name")} />
              {form.formState.errors.name ? (
                <p className="text-xs text-destructive">{form.formState.errors.name.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor={`base-${channel.id}`}>Base URL</Label>
              <Input id={`base-${channel.id}`} className="font-mono" {...form.register("baseUrl")} />
              {form.formState.errors.baseUrl ? (
                <p className="text-xs text-destructive">{form.formState.errors.baseUrl.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor={`key-${channel.id}`}>Rotate API key</Label>
              <Input
                id={`key-${channel.id}`}
                type="password"
                className="font-mono"
                placeholder="留空表示不修改"
                autoComplete="off"
                {...form.register("apiKey")}
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor={`groups-${channel.id}`}>Allow groups</Label>
              <div className="flex gap-2">
                <Input
                  id={`groups-${channel.id}`}
                  placeholder="default, team-a, beta"
                  className="font-mono"
                  value={groupDraft}
                  onChange={(e) => setGroupDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addGroupsFromDraft();
                    }
                  }}
                />
                <Button type="button" variant="outline" className="rounded-xl" onClick={addGroupsFromDraft}>
                  Add
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">留空表示所有分组都可用。</p>
              {allowGroups.length > 0 ? (
                <div className="flex flex-wrap gap-2 pt-1">
                  {allowGroups.map((g) => (
                    <Badge key={g} variant="secondary" className="gap-1 font-mono">
                      {g}
                      <button
                        type="button"
                        className="rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
                        onClick={() =>
                          form.setValue(
                            "allowGroups",
                            allowGroups.filter((x) => x !== g),
                            { shouldValidate: true }
                          )
                        }
                        aria-label={`remove ${g}`}
                      >
                        <X className="h-3 w-3" />
                      </button>
                    </Badge>
                  ))}
                </div>
              ) : null}
            </div>

            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setEditOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!form.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    Saving…
                  </>
                ) : (
                  "Save"
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete channel?</DialogTitle>
            <DialogDescription>将永久删除该渠道及其分组白名单配置。</DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{channel.name}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{channel.baseUrl}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void remove()}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Deleting…
                </>
              ) : (
                "Delete"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

