"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, MoreVertical, Pencil, Power, PowerOff } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AdminModelItem, AdminModelUpdateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
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
  inputUsdPerM: z
    .string()
    .trim()
    .max(32, "价格过长")
    .optional()
    .transform((v) => (v && v.length > 0 ? v : null)),
  outputUsdPerM: z
    .string()
    .trim()
    .max(32, "价格过长")
    .optional()
    .transform((v) => (v && v.length > 0 ? v : null))
});

type FormValues = z.infer<typeof schema>;

interface ModelRowActionsProps {
  model: AdminModelItem;
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

export function ModelRowActions({ model, className }: ModelRowActionsProps) {
  const router = useRouter();
  const [editOpen, setEditOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);

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
    if (!res.ok) throw new Error(readMessage(json, "更新失败"));
    void (json as AdminModelUpdateResponse);
  }

  async function toggleEnabled(nextEnabled: boolean) {
    setSubmitting(true);
    try {
      await patch({ enabled: nextEnabled });
      toast.success(nextEnabled ? "模型已启用" : "模型已关闭");
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function savePricing(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? "表单校验失败");
        return;
      }
      await patch({
        inputUsdPerM: parsed.data.inputUsdPerM,
        outputUsdPerM: parsed.data.outputUsdPerM
      });
      toast.success("价格已更新");
      setEditOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
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
            className={cn("h-9 w-9 rounded-lg", className)}
            aria-label="Model actions"
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
                Disable
              </>
            ) : (
              <>
                <Power className="mr-2 h-4 w-4" />
                Enable
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
            Set pricing
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Model pricing</DialogTitle>
            <DialogDescription>按 $/M tokens 计价；留空表示未配置。</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(savePricing)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`in-${model.model}`}>Input ($/M tokens)</Label>
              <Input
                id={`in-${model.model}`}
                placeholder="例如：0.15"
                className="font-mono"
                {...form.register("inputUsdPerM")}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`out-${model.model}`}>Output ($/M tokens)</Label>
              <Input
                id={`out-${model.model}`}
                placeholder="例如：0.60"
                className="font-mono"
                {...form.register("outputUsdPerM")}
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setEditOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={submitting}>
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
    </>
  );
}
