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

const nameSchema = z
  .string()
  .trim()
  .min(2, "至少 2 个字符")
  .max(64, "最多 64 个字符");

const schema = z.object({
  name: nameSchema
});

type FormValues = z.infer<typeof schema>;

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
  triggerLabel = "创建 Key",
  triggerClassName
}: CreateKeyDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [createdKey, setCreatedKey] = React.useState<string | null>(null);
  const [creating, setCreating] = React.useState(false);

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
        toast.error(firstIssue?.message ?? "表单校验失败");
        return;
      }

      const res = await fetch("/api/keys", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
      });
      if (!res.ok) {
        let message = "创建失败";
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
      toast.success("API Key 已创建");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "创建失败");
    } finally {
      setCreating(false);
    }
  }

  async function copyKey(key: string) {
    try {
      await navigator.clipboard.writeText(key);
      toast.success("已复制到剪贴板");
    } catch {
      toast.error("复制失败，请手动复制");
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
          {triggerLabel}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>创建 API Key</DialogTitle>
          <DialogDescription>
            请妥善保存 Key；你也可以在列表中随时复制完整 Key。
          </DialogDescription>
        </DialogHeader>

        {createdKey ? (
          <div className="space-y-3">
            <div className="rounded-xl border border-border bg-muted/30 p-3">
              <div className="text-xs text-muted-foreground">Your API Key</div>
              <div className="mt-2 break-all font-mono text-sm">{createdKey}</div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="secondary"
                onClick={() => copyKey(createdKey)}
              >
                <Copy className="h-4 w-4" />
                复制
              </Button>
              <Button
                type="button"
                onClick={() => {
                  setOpen(false);
                  reset();
                }}
              >
                完成
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
              <Label htmlFor="name">名称</Label>
              <Input
                id="name"
                placeholder="例如：prod-default"
                autoComplete="off"
                {...form.register("name", {
                  validate: (value) => {
                    const r = nameSchema.safeParse(value);
                    return r.success ? true : (r.error.issues[0]?.message ?? "名称不合法");
                  }
                })}
              />
              {form.formState.errors.name ? (
                <p className="text-xs text-destructive">
                  {form.formState.errors.name.message}
                </p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  用于区分环境与用途。
                </p>
              )}
            </div>

            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                取消
              </Button>
              <Button type="submit" disabled={!canSubmit}>
                {creating ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    创建中…
                  </>
                ) : (
                  "创建"
                )}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
