"use client";

import * as React from "react";
import { z } from "zod";
import { useForm } from "react-hook-form";
import { Loader2, MoreVertical, Pencil, Plus, Tag, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { EmptyState } from "@/components/common/empty-state";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { formatDiscountPercentOff, formatDiscountZhe } from "@/lib/format";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import type {
  AdminModelPricingItem,
  AdminModelPricingUpdateResponse,
  AdminModelPricingUpsertRequest
} from "@/lib/types";
import { dispatchUiEvent, UI_EVENTS } from "@/lib/ui-events";

const DECIMAL_RE = /^\d+(?:\.\d+)?$/;

interface PricingFormValues {
  prefix: string;
  inputUsdPerMOriginal: string;
  outputUsdPerMOriginal: string;
  discount: string;
}

type PricingDialogMode = "create" | "edit";

function createSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  const priceField = z
    .string()
    .trim()
    .max(32, t("validation.priceTooLong"))
    .transform((v) => (v.length > 0 ? v : null))
    .refine((v) => v === null || DECIMAL_RE.test(v), t("validation.priceInvalid"));

  const discountField = z
    .string()
    .trim()
    .max(32, t("validation.priceTooLong"))
    .transform((v) => (v.length > 0 ? v : null))
    .refine((v) => {
      if (v === null) return true;
      if (!DECIMAL_RE.test(v)) return false;
      const n = Number(v);
      return Number.isFinite(n) && n > 0 && n <= 1;
    }, t("validation.discountInvalid"));

  return z
    .object({
      prefix: z
        .string()
        .trim()
        .min(1, t("validation.minChars", { min: 1 }))
        .max(200, t("validation.maxChars", { max: 200 }))
        .refine((v) => !v.includes("\n") && !v.includes("\r"), t("validation.groupInvalidChars")),
      inputUsdPerMOriginal: priceField,
      outputUsdPerMOriginal: priceField,
      discount: discountField
    })
    .superRefine((value, ctx) => {
      if (value.inputUsdPerMOriginal !== null || value.outputUsdPerMOriginal !== null) return;
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["inputUsdPerMOriginal"],
        message: t("validation.priceRequired")
      });
    });
}

function formValuesFromItem(item: AdminModelPricingItem | null): PricingFormValues {
  return {
    prefix: item?.prefix ?? "",
    inputUsdPerMOriginal: item?.inputUsdPerMOriginal ?? "",
    outputUsdPerMOriginal: item?.outputUsdPerMOriginal ?? "",
    discount: typeof item?.discount === "number" ? String(item.discount) : ""
  };
}

function isPricingUpdateResponse(value: unknown): value is AdminModelPricingUpdateResponse {
  if (!value || typeof value !== "object") return false;
  return "item" in value;
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  const obj = json as { message?: unknown; detail?: unknown };
  if (typeof obj.message === "string" && obj.message.length > 0) return obj.message;
  if (typeof obj.detail === "string" && obj.detail.length > 0) return obj.detail;
  return fallback;
}

function pricingRuleUrl(prefix: string) {
  return `/api/admin/model-pricing/${prefix.split("/").map(encodeURIComponent).join("/")}`;
}

function sortPricingItems(items: AdminModelPricingItem[]) {
  return [...items].sort((a, b) => b.prefix.length - a.prefix.length || a.prefix.localeCompare(b.prefix));
}

function formatUsdPerM(value: string | null | undefined) {
  if (!value) return "—";
  return `$${value}`;
}

interface PricePairProps {
  input: string | null | undefined;
  output: string | null | undefined;
}

function PricePair({ input, output }: PricePairProps) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-xs tabular-nums text-foreground">
      <span>{formatUsdPerM(input)}</span>
      <span className="text-muted-foreground">/</span>
      <span>{formatUsdPerM(output)}</span>
    </div>
  );
}

interface DiscountValueProps {
  discount: number | null | undefined;
}

function DiscountValue({ discount }: DiscountValueProps) {
  const { locale, t } = useI18n();
  const hasDiscount = typeof discount === "number" && discount > 0 && discount < 1;
  if (!hasDiscount) return <span className="text-xs text-muted-foreground">—</span>;

  const pct = formatDiscountPercentOff(discount);
  const zhe = formatDiscountZhe(discount);
  const label =
    locale === "zh-CN"
      ? zhe
        ? t("models.discountBadge", { zhe })
        : null
      : pct
        ? t("models.discountBadge", { pct })
        : null;

  return label ? (
    <Badge variant="success" className="rounded-full px-2 py-0 text-[10px]">
      {label}
    </Badge>
  ) : (
    <span className="text-xs text-muted-foreground">—</span>
  );
}

interface ModelPricingRuleDialogProps {
  open: boolean;
  mode: PricingDialogMode;
  item: AdminModelPricingItem | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (item: AdminModelPricingItem, previousPrefix?: string) => void;
}

function ModelPricingRuleDialog({ open, mode, item, onOpenChange, onSaved }: ModelPricingRuleDialogProps) {
  const { t } = useI18n();
  const [submitting, setSubmitting] = React.useState(false);
  const schema = React.useMemo(() => createSchema(t), [t]);
  const form = useForm<PricingFormValues>({
    defaultValues: formValuesFromItem(item),
    mode: "onChange"
  });

  React.useEffect(() => {
    if (!open) return;
    form.reset(formValuesFromItem(item));
  }, [form, item, open]);

  async function save(values: PricingFormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? t("common.formInvalid"));
        return;
      }

      const payload: AdminModelPricingUpsertRequest = {
        prefix: parsed.data.prefix,
        inputUsdPerMOriginal: parsed.data.inputUsdPerMOriginal,
        outputUsdPerMOriginal: parsed.data.outputUsdPerMOriginal,
        discount: parsed.data.discount === null ? null : Number(parsed.data.discount)
      };
      const res = await fetch(mode === "create" ? "/api/admin/model-pricing" : pricingRuleUrl(item?.prefix ?? ""), {
        method: mode === "create" ? "POST" : "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload)
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("common.updateFailed")));
      if (!isPricingUpdateResponse(json)) throw new Error(t("common.unexpectedError"));

      onSaved(json.item, mode === "edit" ? item?.prefix : undefined);
      dispatchUiEvent(UI_EVENTS.adminModelsRefreshed);
      toast.success(mode === "create" ? t("admin.modelPricing.toast.created") : t("admin.modelPricing.toast.updated"));
      onOpenChange(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.updateFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Tag className="h-5 w-5 text-primary" />
            {mode === "create" ? t("admin.modelPricing.createTitle") : t("admin.modelPricing.editTitle")}
          </DialogTitle>
          <DialogDescription>
            {mode === "create" ? t("admin.modelPricing.createDesc") : t("admin.modelPricing.editDesc")}
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            void form.handleSubmit(save)(e);
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="model-pricing-prefix">{t("admin.modelPricing.form.prefix")}</Label>
            <Input
              id="model-pricing-prefix"
              className="font-mono"
              placeholder={t("admin.modelPricing.form.prefixPlaceholder")}
              autoComplete="off"
              {...form.register("prefix")}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="model-pricing-input">{t("admin.modelPricing.form.inputOriginal")}</Label>
              <Input
                id="model-pricing-input"
                className="font-mono"
                inputMode="decimal"
                placeholder={t("admin.models.pricing.inputPlaceholder")}
                {...form.register("inputUsdPerMOriginal")}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="model-pricing-output">{t("admin.modelPricing.form.outputOriginal")}</Label>
              <Input
                id="model-pricing-output"
                className="font-mono"
                inputMode="decimal"
                placeholder={t("admin.models.pricing.outputPlaceholder")}
                {...form.register("outputUsdPerMOriginal")}
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="model-pricing-discount">{t("admin.modelPricing.form.discount")}</Label>
            <Input
              id="model-pricing-discount"
              className="font-mono"
              inputMode="decimal"
              placeholder={t("admin.modelPricing.form.discountPlaceholder")}
              {...form.register("discount")}
            />
            <p className="text-xs text-muted-foreground">{t("admin.modelPricing.form.discountHelp")}</p>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? (
                <span className="inline-flex animate-spin">
                  <Loader2 className="h-4 w-4" />
                </span>
              ) : null}
              {submitting ? t("common.saving") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface DeletePricingRuleDialogProps {
  item: AdminModelPricingItem | null;
  onOpenChange: (open: boolean) => void;
  onDeleted: (prefix: string) => void;
}

function DeletePricingRuleDialog({ item, onOpenChange, onDeleted }: DeletePricingRuleDialogProps) {
  const { t } = useI18n();
  const [submitting, setSubmitting] = React.useState(false);

  async function remove() {
    if (!item) return;
    setSubmitting(true);
    try {
      const res = await fetch(pricingRuleUrl(item.prefix), { method: "DELETE" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("common.deleteFailed")));

      onDeleted(item.prefix);
      dispatchUiEvent(UI_EVENTS.adminModelsRefreshed);
      toast.success(t("admin.modelPricing.toast.deleted"));
      onOpenChange(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.deleteFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={item !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("admin.modelPricing.deleteTitle")}</DialogTitle>
          <DialogDescription>{t("admin.modelPricing.deleteDesc")}</DialogDescription>
        </DialogHeader>
        <div className="rounded-xl border border-border bg-muted/20 p-3 font-mono text-sm text-foreground">
          {item?.prefix}
        </div>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button type="button" variant="destructive" disabled={submitting} onClick={() => void remove()}>
            {submitting ? (
              <span className="inline-flex animate-spin">
                <Loader2 className="h-4 w-4" />
              </span>
            ) : (
              <Trash2 className="h-4 w-4" />
            )}
            {t("common.delete")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface PricingRowActionsProps {
  item: AdminModelPricingItem;
  onEdit: (item: AdminModelPricingItem) => void;
  onDelete: (item: AdminModelPricingItem) => void;
}

function PricingRowActions({ item, onEdit, onDelete }: PricingRowActionsProps) {
  const { t } = useI18n();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" size="icon" variant="ghost" className="h-8 w-8 rounded-xl" aria-label={t("common.actions")}>
          <MoreVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            onEdit(item);
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
            onDelete(item);
          }}
        >
          <Trash2 className="mr-2 h-4 w-4" />
          {t("common.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

interface AdminModelPricingTableClientProps {
  initialItems: AdminModelPricingItem[];
}

export function AdminModelPricingTableClient({ initialItems }: AdminModelPricingTableClientProps) {
  const { t } = useI18n();
  const [items, setItems] = React.useState<AdminModelPricingItem[]>(() => sortPricingItems(initialItems));
  const [dialogMode, setDialogMode] = React.useState<PricingDialogMode | null>(null);
  const [editingItem, setEditingItem] = React.useState<AdminModelPricingItem | null>(null);
  const [deletingItem, setDeletingItem] = React.useState<AdminModelPricingItem | null>(null);

  function openCreate() {
    setEditingItem(null);
    setDialogMode("create");
  }

  function openEdit(item: AdminModelPricingItem) {
    setEditingItem(item);
    setDialogMode("edit");
  }

  function closeForm(open: boolean) {
    if (open) return;
    setDialogMode(null);
    setEditingItem(null);
  }

  function upsert(next: AdminModelPricingItem, previousPrefix?: string) {
    setItems((prev) => {
      const filtered =
        previousPrefix && previousPrefix !== next.prefix ? prev.filter((item) => item.prefix !== previousPrefix) : prev;
      const idx = filtered.findIndex((item) => item.prefix === next.prefix);
      if (idx === -1) return sortPricingItems([next, ...filtered]);
      const copy = [...filtered];
      copy[idx] = next;
      return sortPricingItems(copy);
    });
  }

  function remove(prefix: string) {
    setItems((prev) => prev.filter((item) => item.prefix !== prefix));
  }

  return (
    <Card>
      <CardHeader className="gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1.5">
          <CardTitle>{t("admin.modelPricing.title")}</CardTitle>
          <CardDescription>{t("admin.modelPricing.desc")}</CardDescription>
        </div>
        <Button type="button" className="rounded-xl sm:shrink-0" onClick={openCreate}>
          <Plus className="h-4 w-4" />
          {t("admin.modelPricing.add")}
        </Button>
      </CardHeader>

      {items.length === 0 ? (
        <CardContent>
          <EmptyState
            icon={<Tag className="h-6 w-6 text-muted-foreground" />}
            title={t("admin.modelPricing.empty")}
            description={t("admin.modelPricing.emptyDesc")}
            action={(
              <Button type="button" className="rounded-xl" onClick={openCreate}>
                <Plus className="h-4 w-4" />
                {t("admin.modelPricing.add")}
              </Button>
            )}
          />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table variant="card">
            <TableHeader>
              <TableRow>
                <TableHead>{t("admin.modelPricing.table.prefix")}</TableHead>
                <TableHead>{t("admin.modelPricing.table.original")}</TableHead>
                <TableHead>{t("admin.modelPricing.table.discount")}</TableHead>
                <TableHead>{t("admin.modelPricing.table.effective")}</TableHead>
                <TableHead className="w-12 text-right">{t("keys.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.prefix} className="uai-cv-auto">
                  <TableCell className="max-w-[320px] truncate font-mono text-xs text-foreground">
                    {item.prefix}
                  </TableCell>
                  <TableCell>
                    <PricePair input={item.inputUsdPerMOriginal} output={item.outputUsdPerMOriginal} />
                  </TableCell>
                  <TableCell>
                    <DiscountValue discount={item.discount} />
                  </TableCell>
                  <TableCell>
                    <PricePair input={item.inputUsdPerM} output={item.outputUsdPerM} />
                  </TableCell>
                  <TableCell className="text-right">
                    <PricingRowActions item={item} onEdit={openEdit} onDelete={setDeletingItem} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}

      <ModelPricingRuleDialog
        open={dialogMode !== null}
        mode={dialogMode ?? "create"}
        item={editingItem}
        onOpenChange={closeForm}
        onSaved={upsert}
      />
      <DeletePricingRuleDialog
        item={deletingItem}
        onOpenChange={(open) => {
          if (!open) setDeletingItem(null);
        }}
        onDeleted={remove}
      />
    </Card>
  );
}
