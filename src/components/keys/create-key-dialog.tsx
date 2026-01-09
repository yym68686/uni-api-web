"use client";

import * as React from "react";
import { Copy, KeyRound, Loader2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import type { ApiKeyCreateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
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
import { useI18n } from "@/components/i18n/i18n-provider";

import type { MessageKey, MessageVars } from "@/lib/i18n/messages";

function createNameSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z
    .string()
    .trim()
    .min(2, t("validation.minChars", { min: 2 }))
    .max(64, t("validation.maxChars", { max: 64 }));
}

function createSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.object({
    name: createNameSchema(t)
  });
}

type FormValues = z.infer<ReturnType<typeof createSchema>>;

interface CreateKeyDialogProps {
  onCreated: (res: ApiKeyCreateResponse) => void;
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

export function CreateKeyDialog({
  onCreated,
  triggerLabel,
  triggerClassName
}: CreateKeyDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [createdKey, setCreatedKey] = React.useState<string | null>(null);
  const [creating, setCreating] = React.useState(false);
  const { t } = useI18n();

  const nameSchema = React.useMemo(() => createNameSchema(t), [t]);
  const schema = React.useMemo(() => createSchema(t), [t]);

  const triggerText = triggerLabel ?? t("keys.create");

  const form = useForm<FormValues>({
    defaultValues: { name: "" },
    mode: "onChange"
  });

  const canSubmit = form.formState.isValid && !creating;

  async function onSubmit(values: FormValues) {
    setCreating(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        const firstIssue = parsed.error.issues[0];
        if (firstIssue?.path[0] === "name") {
          form.setError("name", { message: firstIssue.message, type: "validate" });
        }
        toast.error(firstIssue?.message ?? t("keys.toast.updateFailed"));
        return;
      }

      const res = await fetch("/api/keys", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
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
      onCreated(json);
      toast.success(t("keys.dialog.created"));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("keys.toast.updateFailed"));
    } finally {
      setCreating(false);
    }
  }

  async function copyKey(key: string) {
    try {
      await navigator.clipboard.writeText(key);
      toast.success(t("keys.dialog.copySuccess"));
    } catch {
      toast.error(t("keys.dialog.copyFailed"));
    }
  }

  function reset() {
    setCreatedKey(null);
    form.reset({ name: "" });
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button className={cn("rounded-xl", triggerClassName)}>
          <KeyRound className="h-4 w-4" />
          {triggerText}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("keys.dialog.title")}</DialogTitle>
          <DialogDescription>
            {t("keys.dialog.desc")}
          </DialogDescription>
        </DialogHeader>

        {createdKey ? (
          <div className="space-y-3">
            <div className="rounded-xl border border-border bg-muted/30 p-3">
              <div className="text-xs text-muted-foreground">{t("keys.dialog.yourKey")}</div>
              <div className="mt-2 break-all font-mono tabular-nums tracking-wide text-sm">{createdKey}</div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="secondary"
                onClick={() => copyKey(createdKey)}
              >
                <Copy className="h-4 w-4" />
                {t("keys.dialog.copy")}
              </Button>
              <Button
                type="button"
                onClick={() => {
                  setOpen(false);
                  reset();
                }}
              >
                {t("keys.dialog.done")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(onSubmit)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="name">{t("keys.dialog.name")}</Label>
              <Input
                id="name"
                placeholder={t("keys.dialog.namePlaceholder")}
                autoComplete="off"
                {...form.register("name", {
                validate: (value) => {
                  const r = nameSchema.safeParse(value);
                  return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                }
              })}
            />
              {form.formState.errors.name ? (
                <p className="text-xs text-destructive">
                  {form.formState.errors.name.message}
                </p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  {t("keys.dialog.nameHelp")}
                </p>
              )}
            </div>

            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                {t("keys.dialog.cancel")}
              </Button>
              <Button type="submit" disabled={!canSubmit}>
                {creating ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    {t("keys.dialog.creating")}
                  </>
                ) : (
                  t("keys.dialog.create")
                )}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
