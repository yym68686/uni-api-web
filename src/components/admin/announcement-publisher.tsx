"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Loader2, Megaphone, Plus } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AnnouncementCreateResponse } from "@/lib/types";
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

const schema = z.object({
  title: z.string().trim().min(2, "至少 2 个字符").max(180, "最多 180 个字符"),
  meta: z.string().trim().min(2, "至少 2 个字符").max(120, "最多 120 个字符"),
  level: z.enum(["info", "warning", "success", "destructive"])
});

type FormValues = z.infer<typeof schema>;

interface AnnouncementPublisherProps {
  onCreated?: () => void;
  className?: string;
}

const glow =
  "shadow-[0_0_0_1px_oklch(var(--primary)/0.25),0_12px_30px_oklch(var(--primary)/0.22)] hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_16px_40px_oklch(var(--primary)/0.28)]";

export function AnnouncementPublisher({ onCreated, className }: AnnouncementPublisherProps) {
  const [open, setOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const router = useRouter();

  const form = useForm<FormValues>({
    defaultValues: { title: "", meta: "", level: "warning" },
    mode: "onChange"
  });

  function reset() {
    form.reset({ title: "", meta: "", level: "warning" });
  }

  async function onSubmit(values: FormValues) {
    setSubmitting(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? "表单校验失败");
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
            ? String((json as { message?: unknown }).message ?? "发布失败")
            : "发布失败";
        throw new Error(message);
      }

      const created = json as AnnouncementCreateResponse;
      toast.success("公告已发布");
      onCreated?.();
      router.refresh();
      setOpen(false);
      reset();
      return created;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "发布失败");
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
        <Button className={cn("rounded-xl", glow, className)}>
          <Plus className="h-4 w-4" />
          Publish
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Megaphone className="h-5 w-5 text-primary" />
            发布公告
          </DialogTitle>
          <DialogDescription>公告会显示在 Dashboard 右侧栏目。</DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            void form.handleSubmit(onSubmit)(e);
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="title">标题</Label>
            <Input id="title" placeholder="例如：新版本发布 / 计费变更" {...form.register("title")} />
            {form.formState.errors.title ? (
              <p className="text-xs text-destructive">{form.formState.errors.title.message}</p>
            ) : null}
          </div>

          <div className="space-y-2">
            <Label htmlFor="meta">Meta</Label>
            <Input id="meta" placeholder="例如：Today · Security" {...form.register("meta")} />
            {form.formState.errors.meta ? (
              <p className="text-xs text-destructive">{form.formState.errors.meta.message}</p>
            ) : (
              <p className="text-xs text-muted-foreground font-mono">
                建议使用 “Today · Category” 格式
              </p>
            )}
          </div>

          <div className="space-y-2">
            <Label>Level</Label>
            <div className="grid grid-cols-4 gap-2">
              {(
                [
                  { value: "info", label: "Info" },
                  { value: "warning", label: "Warn" },
                  { value: "success", label: "Good" },
                  { value: "destructive", label: "Critical" }
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
              Cancel
            </Button>
            <Button type="submit" disabled={!form.formState.isValid || submitting}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Publishing…
                </>
              ) : (
                "Publish"
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
