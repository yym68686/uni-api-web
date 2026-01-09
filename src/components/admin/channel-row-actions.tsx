"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, MoreVertical, Pencil, Shield, Trash2, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { LlmChannelDeleteResponse, LlmChannelItem, LlmChannelUpdateResponse } from "@/lib/types";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
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

function createSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(2, t("validation.minChars", { min: 2 }))
      .max(64, t("validation.maxChars", { max: 64 })),
    baseUrl: z
      .string()
      .trim()
      .min(8, t("validation.baseUrlRequired"))
      .max(400, t("validation.maxChars", { max: 400 }))
      .refine((v) => {
        try {
          const u = new URL(v);
          return u.protocol === "http:" || u.protocol === "https:";
        } catch {
          return false;
        }
      }, t("validation.baseUrlInvalid")),
    apiKey: z.string().trim().min(0).max(4096, t("validation.apiKeyMax")).optional(),
    allowGroups: z
      .array(
        z
          .string()
          .trim()
          .min(1)
          .max(64)
          .refine((v) => !v.includes("\n") && !v.includes("\r"), t("validation.groupInvalidChars"))
      )
      .default([])
  });
}

type FormValues = z.infer<ReturnType<typeof createSchema>>;

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
  const { t } = useI18n();

  const schema = React.useMemo(() => createSchema(t), [t]);

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
        toast.error(parsed.error.issues[0]?.message ?? t("common.formInvalid"));
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
      if (!res.ok) throw new Error(readMessage(json, t("common.updateFailed")));

      void (json as LlmChannelUpdateResponse);
      toast.success(t("admin.channels.toast.updated"));
      setEditOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.updateFailed"));
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
      if (!res.ok) throw new Error(readMessage(json, t("common.deleteFailed")));
      void (json as LlmChannelDeleteResponse);
      toast.success(t("admin.channels.toast.deleted"));
      setDeleteOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.deleteFailed"));
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
            aria-label={t("common.actions")}
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
            {t("common.edit")}
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
            {t("common.delete")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-primary" />
              {t("admin.channels.editTitle")}
            </DialogTitle>
            <DialogDescription>{t("admin.channels.editDesc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(update)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`name-${channel.id}`}>{t("admin.channels.form.name")}</Label>
              <Input id={`name-${channel.id}`} {...form.register("name")} />
              {form.formState.errors.name ? (
                <p className="text-xs text-destructive">{form.formState.errors.name.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor={`base-${channel.id}`}>{t("admin.channels.form.baseUrl")}</Label>
              <Input id={`base-${channel.id}`} className="font-mono" {...form.register("baseUrl")} />
              {form.formState.errors.baseUrl ? (
                <p className="text-xs text-destructive">{form.formState.errors.baseUrl.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor={`key-${channel.id}`}>{t("admin.channels.form.rotateApiKey")}</Label>
              <Input
                id={`key-${channel.id}`}
                type="password"
                className="font-mono"
                placeholder={t("admin.channels.form.rotatePlaceholder")}
                autoComplete="off"
                {...form.register("apiKey")}
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor={`groups-${channel.id}`}>{t("admin.channels.form.allowGroups")}</Label>
              <div className="flex gap-2">
                <Input
                  id={`groups-${channel.id}`}
                  placeholder={t("admin.channels.form.allowGroupsPlaceholder")}
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
                  {t("common.add")}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">{t("admin.channels.form.allowGroupsHelp")}</p>
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
                        aria-label={t("admin.channels.form.removeGroup", { group: g })}
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
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!form.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    {t("common.saving")}
                  </>
                ) : (
                  t("common.save")
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.channels.deleteTitle")}</DialogTitle>
            <DialogDescription>{t("admin.channels.deleteDesc")}</DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{channel.name}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{channel.baseUrl}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void remove()}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t("common.deleting")}
                </>
              ) : (
                t("common.delete")
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
