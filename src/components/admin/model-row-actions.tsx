"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, MoreVertical, Pencil, Power, PowerOff } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AdminModelItem, AdminModelUpdateResponse } from "@/lib/types";
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
    inputUsdPerM: z
      .string()
      .trim()
      .max(32, t("validation.priceTooLong"))
      .optional()
      .transform((v) => (v && v.length > 0 ? v : null)),
    outputUsdPerM: z
      .string()
      .trim()
      .max(32, t("validation.priceTooLong"))
      .optional()
      .transform((v) => (v && v.length > 0 ? v : null))
  });
}

type FormValues = z.infer<ReturnType<typeof createSchema>>;

interface ModelRowActionsProps {
  model: AdminModelItem;
  onUpdated?: (next: AdminModelItem) => void;
  className?: string;
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function ModelRowActions({ model, onUpdated, className }: ModelRowActionsProps) {
  const router = useRouter();
  const [editOpen, setEditOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const { t } = useI18n();

  const schema = React.useMemo(() => createSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: {
      inputUsdPerM: model.inputUsdPerM ?? "",
      outputUsdPerM: model.outputUsdPerM ?? ""
    },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (!editOpen) return;
    form.reset({
      inputUsdPerM: model.inputUsdPerM ?? "",
      outputUsdPerM: model.outputUsdPerM ?? ""
    });
  }, [editOpen, form, model.inputUsdPerM, model.outputUsdPerM]);

  async function patch(payload: Record<string, unknown>) {
    const res = await fetch(`/api/admin/models/${encodeURIComponent(model.model)}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload)
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error(readMessage(json, t("common.updateFailed")));
    const next = json as AdminModelUpdateResponse;
    return next.item;
  }

  async function toggleEnabled(nextEnabled: boolean) {
    setSubmitting(true);
    try {
      onUpdated?.({ ...model, enabled: nextEnabled });
      const next = await patch({ enabled: nextEnabled });
      onUpdated?.(next);
      toast.success(nextEnabled ? t("admin.models.toast.enabled") : t("admin.models.toast.disabled"));
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(model);
      toast.error(err instanceof Error ? err.message : t("common.updateFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function savePricing(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? t("common.formInvalid"));
        return;
      }
      onUpdated?.({
        ...model,
        inputUsdPerM: parsed.data.inputUsdPerM ?? null,
        outputUsdPerM: parsed.data.outputUsdPerM ?? null
      });
      const next = await patch({
        inputUsdPerM: parsed.data.inputUsdPerM,
        outputUsdPerM: parsed.data.outputUsdPerM
      });
      onUpdated?.(next);
      toast.success(t("admin.models.toast.pricingUpdated"));
      setEditOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(model);
      toast.error(err instanceof Error ? err.message : t("common.updateFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className={cn("h-8 w-8 rounded-xl", className)}
            aria-label={t("common.actions")}
          >
            <MoreVertical className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            disabled={submitting}
            onSelect={(e) => {
              e.preventDefault();
              void toggleEnabled(!model.enabled);
            }}
          >
            {model.enabled ? (
              <>
                <PowerOff className="mr-2 h-4 w-4" />
                {t("admin.models.actions.disable")}
              </>
            ) : (
              <>
                <Power className="mr-2 h-4 w-4" />
                {t("admin.models.actions.enable")}
              </>
            )}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault();
              setEditOpen(true);
            }}
          >
            <Pencil className="mr-2 h-4 w-4" />
            {t("admin.models.pricing.set")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.models.pricing.title")}</DialogTitle>
            <DialogDescription>{t("admin.models.pricing.desc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(savePricing)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`in-${model.model}`}>{t("models.table.input")}</Label>
              <Input
                id={`in-${model.model}`}
                placeholder={t("admin.models.pricing.inputPlaceholder")}
                className="font-mono"
                {...form.register("inputUsdPerM")}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`out-${model.model}`}>{t("models.table.output")}</Label>
              <Input
                id={`out-${model.model}`}
                placeholder={t("admin.models.pricing.outputPlaceholder")}
                className="font-mono"
                {...form.register("outputUsdPerM")}
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setEditOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={submitting}>
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
    </>
  );
}
