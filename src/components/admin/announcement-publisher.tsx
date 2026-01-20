"use client";

import * as React from "react";
import { z } from "zod";
import { Loader2, Megaphone, Plus } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AnnouncementCreateResponse, AnnouncementItem } from "@/lib/types";
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
  DialogTitle,
  DialogTrigger
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { dispatchUiEvent, UI_EVENTS } from "@/lib/ui-events";
import { PRIMARY_CTA_CLASSNAME } from "@/lib/ui-styles";

function createSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z
    .object({
      titleZh: z
        .string()
        .trim()
        .max(180, t("validation.maxChars", { max: 180 }))
        .refine((v) => v === "" || v.length >= 2, t("validation.minChars", { min: 2 })),
      titleEn: z
        .string()
        .trim()
        .max(180, t("validation.maxChars", { max: 180 }))
        .refine((v) => v === "" || v.length >= 2, t("validation.minChars", { min: 2 })),
      metaZh: z
        .string()
        .trim()
        .max(120, t("validation.maxChars", { max: 120 }))
        .refine((v) => v === "" || v.length >= 2, t("validation.minChars", { min: 2 })),
      metaEn: z
        .string()
        .trim()
        .max(120, t("validation.maxChars", { max: 120 }))
        .refine((v) => v === "" || v.length >= 2, t("validation.minChars", { min: 2 })),
      level: z.enum(["info", "warning", "success", "destructive"])
    })
    .superRefine((values, ctx) => {
      const hasTitle = Boolean(values.titleZh || values.titleEn);
      const hasMeta = Boolean(values.metaZh || values.metaEn);
      if (!hasTitle) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t("admin.ann.form.needTitle"),
          path: ["titleZh"]
        });
      }
      if (!hasMeta) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t("admin.ann.form.needMeta"),
          path: ["metaZh"]
        });
      }
    });
}

type FormValues = z.infer<ReturnType<typeof createSchema>>;

interface AnnouncementPublisherProps {
  onCreated?: (item: AnnouncementItem) => void;
  className?: string;
}

export function AnnouncementPublisher({ onCreated, className }: AnnouncementPublisherProps) {
  const [open, setOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [activeLang, setActiveLang] = React.useState<"zh" | "en">("zh");
  const { t } = useI18n();

  const schema = React.useMemo(() => createSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: { titleZh: "", titleEn: "", metaZh: "", metaEn: "", level: "warning" },
    mode: "onChange"
  });

  function reset() {
    form.reset({ titleZh: "", titleEn: "", metaZh: "", metaEn: "", level: "warning" });
    setActiveLang("zh");
  }

  async function onSubmit(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? t("common.formInvalid"));
        return;
      }

      const res = await fetch("/api/admin/announcements", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("common.operationFailed"))
            : t("common.operationFailed");
        throw new Error(message);
      }

      const created = json as AnnouncementCreateResponse;
      toast.success(t("admin.ann.toast.published"));
      onCreated?.(created.item);
      dispatchUiEvent(UI_EVENTS.adminAnnouncementsCreated, created.item);
      setOpen(false);
      reset();
      return created;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  const level = form.watch("level");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button className={cn("rounded-xl uai-border-beam", PRIMARY_CTA_CLASSNAME, className)}>
          <Plus className="h-4 w-4" />
          {t("common.publish")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Megaphone className="h-5 w-5 text-primary" />
            {t("admin.ann.publish")}
          </DialogTitle>
          <DialogDescription>{t("admin.ann.publishDesc")}</DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            void form.handleSubmit(onSubmit)(e);
          }}
        >
          <div className="space-y-4">
            <div
              role="tablist"
              aria-label={t("admin.ann.form.title")}
              className="grid grid-cols-2 gap-2 rounded-xl border border-border bg-muted/20 p-1"
            >
              <Button
                type="button"
                role="tab"
                aria-selected={activeLang === "zh"}
                variant={activeLang === "zh" ? "default" : "ghost"}
                className="rounded-lg"
                onClick={() => setActiveLang("zh")}
              >
                {t("common.lang.zh")}
              </Button>
              <Button
                type="button"
                role="tab"
                aria-selected={activeLang === "en"}
                variant={activeLang === "en" ? "default" : "ghost"}
                className="rounded-lg"
                onClick={() => setActiveLang("en")}
              >
                {t("common.lang.en")}
              </Button>
            </div>

            <div
              role="tabpanel"
              aria-label={t("common.lang.zh")}
              className={cn("space-y-4", activeLang === "zh" ? "block" : "hidden")}
            >
              <div className="space-y-2">
                <Label htmlFor="title-zh">{t("admin.ann.form.title")}</Label>
                <Input
                  id="title-zh"
                  placeholder="例如：新版本发布 / 计费变更"
                  {...form.register("titleZh")}
                />
                {form.formState.errors.titleZh ? (
                  <p className="text-xs text-destructive">{form.formState.errors.titleZh.message}</p>
                ) : null}
              </div>

              <div className="space-y-2">
                <Label htmlFor="meta-zh">{t("admin.ann.form.meta")}</Label>
                <Input id="meta-zh" placeholder="例如：今天 · 安全" {...form.register("metaZh")} />
                {form.formState.errors.metaZh ? (
                  <p className="text-xs text-destructive">{form.formState.errors.metaZh.message}</p>
                ) : (
                  <p className="text-xs text-muted-foreground font-mono">建议格式：今天 · 分类</p>
                )}
              </div>
            </div>

            <div
              role="tabpanel"
              aria-label={t("common.lang.en")}
              className={cn("space-y-4", activeLang === "en" ? "block" : "hidden")}
            >
              <div className="space-y-2">
                <Label htmlFor="title-en">{t("admin.ann.form.title")}</Label>
                <Input
                  id="title-en"
                  placeholder="e.g. New release / Billing changes"
                  {...form.register("titleEn")}
                />
                {form.formState.errors.titleEn ? (
                  <p className="text-xs text-destructive">{form.formState.errors.titleEn.message}</p>
                ) : null}
              </div>

              <div className="space-y-2">
                <Label htmlFor="meta-en">{t("admin.ann.form.meta")}</Label>
                <Input id="meta-en" placeholder="e.g. Today · Security" {...form.register("metaEn")} />
                {form.formState.errors.metaEn ? (
                  <p className="text-xs text-destructive">{form.formState.errors.metaEn.message}</p>
                ) : (
                  <p className="text-xs text-muted-foreground font-mono">Suggested format: “Today · Category”</p>
                )}
              </div>
            </div>
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
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!form.formState.isValid || submitting}>
              {submitting ? (
                <>
                  <span className="inline-flex animate-spin">
                    <Loader2 className="h-4 w-4" />
                  </span>
                  {t("common.publishing")}
                </>
              ) : (
                t("common.publish")
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
