"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, PlugZap, Plus, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { LlmChannelCreateResponse } from "@/lib/types";
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
import { Badge } from "@/components/ui/badge";

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
  apiKey: z.string().trim().min(8, "API key 至少 8 个字符").max(4096, "API key 过长"),
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
        toast.error(parsed.error.issues[0]?.message ?? "表单校验失败");
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
            ? String((json as { message?: unknown }).message ?? "创建失败")
            : "创建失败";
        throw new Error(message);
      }

      void (json as LlmChannelCreateResponse);
      toast.success("渠道已创建");
      router.refresh();
      setOpen(false);
      reset();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "创建失败");
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
          Create channel
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PlugZap className="h-5 w-5 text-primary" />
            新增渠道
          </DialogTitle>
          <DialogDescription>填写 Base URL 与 API key，并设置允许使用的分组。</DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            void form.handleSubmit(onSubmit)(e);
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="channel-name">Name</Label>
            <Input id="channel-name" placeholder="例如：OpenAI / DeepSeek / Local" {...form.register("name")} />
            {form.formState.errors.name ? (
              <p className="text-xs text-destructive">{form.formState.errors.name.message}</p>
            ) : null}
          </div>

          <div className="space-y-2">
            <Label htmlFor="channel-base-url">Base URL</Label>
            <Input
              id="channel-base-url"
              placeholder="https://api.example.com/v1"
              className="font-mono"
              {...form.register("baseUrl")}
            />
            {form.formState.errors.baseUrl ? (
              <p className="text-xs text-destructive">{form.formState.errors.baseUrl.message}</p>
            ) : (
              <p className="text-xs text-muted-foreground font-mono">会自动去掉末尾的 “/”。</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="channel-api-key">API key</Label>
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
            <Label htmlFor="channel-groups">Allow groups</Label>
            <div className="flex gap-2">
              <Input
                id="channel-groups"
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
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!form.formState.isValid || submitting}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Creating…
                </>
              ) : (
                "Create"
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

