"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, PlugZap, Plus, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { LlmChannelCreateResponse } from "@/lib/types";
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
import { Badge } from "@/components/ui/badge";

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
    apiKey: z
      .string()
      .trim()
      .min(8, t("validation.apiKeyMin", { min: 8 }))
      .max(4096, t("validation.apiKeyMax")),
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

interface ChannelPublisherProps {
  className?: string;
}

const glow =
  "shadow-[0_0_0_1px_oklch(var(--primary)/0.25),0_12px_30px_oklch(var(--primary)/0.22)] hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_16px_40px_oklch(var(--primary)/0.28)]";

function normalizeGroups(value: string[]) {
  const unique = new Set<string>();
  for (const raw of value) {
    const v = raw.trim();
    if (!v) continue;
    unique.add(v);
  }
  return Array.from(unique);
}

export function ChannelPublisher({ className }: ChannelPublisherProps) {
  const [open, setOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [groupDraft, setGroupDraft] = React.useState("");
  const router = useRouter();
  const { t } = useI18n();

  const schema = React.useMemo(() => createSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: { name: "", baseUrl: "", apiKey: "", allowGroups: [] },
    mode: "onChange"
  });

  function reset() {
    form.reset({ name: "", baseUrl: "", apiKey: "", allowGroups: [] });
    setGroupDraft("");
  }

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

  async function onSubmit(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? t("common.formInvalid"));
        return;
      }

      const res = await fetch("/api/admin/channels", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("common.createFailed"))
            : t("common.createFailed");
        throw new Error(message);
      }

      void (json as LlmChannelCreateResponse);
      toast.success(t("admin.channels.toast.created"));
      router.refresh();
      setOpen(false);
      reset();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.createFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  const allowGroups = form.watch("allowGroups");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button className={cn("rounded-xl", glow, className)}>
          <Plus className="h-4 w-4" />
          {t("admin.channels.create")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PlugZap className="h-5 w-5 text-primary" />
            {t("admin.channels.createTitle")}
          </DialogTitle>
          <DialogDescription>{t("admin.channels.createDesc")}</DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            void form.handleSubmit(onSubmit)(e);
          }}
          >
            <div className="space-y-2">
              <Label htmlFor="channel-name">{t("admin.channels.form.name")}</Label>
              <Input
                id="channel-name"
                placeholder={t("admin.channels.form.namePlaceholder")}
                {...form.register("name")}
              />
              {form.formState.errors.name ? (
                <p className="text-xs text-destructive">{form.formState.errors.name.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor="channel-base-url">{t("admin.channels.form.baseUrl")}</Label>
              <Input
                id="channel-base-url"
                placeholder={t("admin.channels.form.baseUrlPlaceholder")}
                className="font-mono"
                {...form.register("baseUrl")}
              />
              {form.formState.errors.baseUrl ? (
                <p className="text-xs text-destructive">{form.formState.errors.baseUrl.message}</p>
              ) : (
                <p className="text-xs text-muted-foreground font-mono">
                  {t("admin.channels.form.baseUrlHelp")}
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="channel-api-key">{t("admin.channels.form.apiKey")}</Label>
              <Input
                id="channel-api-key"
                type="password"
                placeholder="••••••••"
                className="font-mono"
                autoComplete="off"
                {...form.register("apiKey")}
              />
              {form.formState.errors.apiKey ? (
                <p className="text-xs text-destructive">{form.formState.errors.apiKey.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor="channel-groups">{t("admin.channels.form.allowGroups")}</Label>
              <div className="flex gap-2">
                <Input
                  id="channel-groups"
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
                <Button
                  type="button"
                  variant="outline"
                  className="rounded-xl"
                  onClick={addGroupsFromDraft}
                >
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
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!form.formState.isValid || submitting}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t("common.creating")}
                </>
              ) : (
                t("common.create")
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
