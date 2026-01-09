"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, MoreVertical, Pencil, Trash2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AnnouncementDeleteResponse, AnnouncementItem, AnnouncementUpdateResponse } from "@/lib/types";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
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
    title: z
      .string()
      .trim()
      .min(2, t("validation.minChars", { min: 2 }))
      .max(180, t("validation.maxChars", { max: 180 })),
    meta: z
      .string()
      .trim()
      .min(2, t("validation.minChars", { min: 2 }))
      .max(120, t("validation.maxChars", { max: 120 })),
    level: z.enum(["info", "warning", "success", "destructive"])
  });
}

type FormValues = z.infer<ReturnType<typeof createSchema>>;

interface AnnouncementRowActionsProps {
  announcement: AnnouncementItem;
  className?: string;
}

function normalizeLevel(level: AnnouncementItem["level"]): FormValues["level"] {
  switch (level) {
    case "info":
    case "warning":
    case "success":
    case "destructive":
      return level;
    default:
      return "warning";
  }
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function AnnouncementRowActions({ announcement, className }: AnnouncementRowActionsProps) {
  const router = useRouter();
  const [editOpen, setEditOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const { t } = useI18n();

  const schema = React.useMemo(() => createSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: {
      title: announcement.title,
      meta: announcement.meta,
      level: normalizeLevel(announcement.level)
    },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (!editOpen) return;
    form.reset({
      title: announcement.title,
      meta: announcement.meta,
      level: normalizeLevel(announcement.level)
    });
  }, [announcement.level, announcement.meta, announcement.title, editOpen, form]);

  async function update(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? t("common.formInvalid"));
        return;
      }

      const res = await fetch(`/api/admin/announcements/${encodeURIComponent(announcement.id)}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("common.updateFailed")));

      void (json as AnnouncementUpdateResponse);
      toast.success(t("admin.ann.toast.updated"));
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
      const res = await fetch(`/api/admin/announcements/${encodeURIComponent(announcement.id)}`, {
        method: "DELETE"
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("common.deleteFailed")));

      void (json as AnnouncementDeleteResponse);
      toast.success(t("admin.ann.toast.deleted"));
      setDeleteOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.deleteFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  const level = form.watch("level");

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
            <DialogTitle>{t("admin.ann.editTitle")}</DialogTitle>
            <DialogDescription>{t("admin.ann.editDesc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(update)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`title-${announcement.id}`}>{t("admin.ann.form.title")}</Label>
              <Input id={`title-${announcement.id}`} {...form.register("title")} />
              {form.formState.errors.title ? (
                <p className="text-xs text-destructive">{form.formState.errors.title.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor={`meta-${announcement.id}`}>{t("admin.ann.form.meta")}</Label>
              <Input id={`meta-${announcement.id}`} {...form.register("meta")} />
              {form.formState.errors.meta ? (
                <p className="text-xs text-destructive">{form.formState.errors.meta.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label>{t("admin.ann.form.level")}</Label>
              <div className="grid grid-cols-4 gap-2">
                {(
                  [
                    { value: "info", label: t("admin.ann.level.info") },
                    { value: "warning", label: t("admin.ann.level.warning") },
                    { value: "success", label: t("admin.ann.level.success") },
                    { value: "destructive", label: t("admin.ann.level.destructive") }
                  ] as const
                ).map((opt) => {
                  const active = level === opt.value;
                  return (
                    <Button
                      key={opt.value}
                      type="button"
                      variant={active ? "default" : "outline"}
                      className="rounded-xl"
                      onClick={() => form.setValue("level", opt.value, { shouldValidate: true })}
                    >
                      {opt.label}
                    </Button>
                  );
                })}
              </div>
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
            <DialogTitle>{t("admin.ann.deleteTitle")}</DialogTitle>
            <DialogDescription>
              {t("admin.ann.deleteDesc")}
            </DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{announcement.title}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{announcement.meta}</div>
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
