"use client";

import * as React from "react";
import Link from "next/link";
import { z } from "zod";
import { Chrome, Github, Loader2, Lock, Mail } from "lucide-react";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { BrandWordmark } from "@/components/brand/wordmark";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";

function createLoginSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  const emailSchema = z.string().trim().email(t("validation.email"));
  const passwordSchema = z
    .string()
    .min(6, t("validation.passwordMin", { min: 6 }))
    .max(128, t("validation.passwordMax"));
  return z.object({
    email: emailSchema,
    password: passwordSchema
  });
}

type FormValues = z.infer<ReturnType<typeof createLoginSchema>>;

interface LoginFormProps {
  appName: string;
  nextPath?: string;
  className?: string;
}

export function LoginForm({ appName, nextPath, className }: LoginFormProps) {
  const [loading, setLoading] = React.useState(false);
  const router = useRouter();
  const { t } = useI18n();

  const schema = React.useMemo(() => createLoginSchema(t), [t]);
  const emailSchema = schema.shape.email;
  const passwordSchema = schema.shape.password;

  const form = useForm<FormValues>({
    defaultValues: { email: "", password: "" },
    mode: "onChange"
  });

  async function onSubmit(values: FormValues) {
    setLoading(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        const issues = parsed.error.issues;
        for (const issue of issues) {
          const key = issue.path[0];
          if (key === "email") form.setError("email", { message: issue.message, type: "validate" });
          if (key === "password") form.setError("password", { message: issue.message, type: "validate" });
        }
        toast.error(issues[0]?.message ?? t("login.failed"));
        return;
      }

      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed.data)
      });
      if (!res.ok) {
        const json: unknown = await res.json().catch(() => null);
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("login.failed"))
            : t("login.failed");
        throw new Error(message);
      }

      toast.success(t("login.success"));
      const next = nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard";
      router.replace(next);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("login.failed"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className={cn("w-full max-w-sm", className)}>
      <div className="text-center">
        <BrandWordmark name={appName} className="text-lg" />
        <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground">
          {t("login.title")}
        </div>
        <p className="mt-2 text-sm text-muted-foreground">
          {t("login.noAccount")}{" "}
          <Link
            href={nextPath ? `/register?next=${encodeURIComponent(nextPath)}` : "/register"}
            className="text-primary hover:underline"
          >
            {t("login.createOne")}
          </Link>
        </p>
      </div>

      <form
        className="mt-8 space-y-4"
        onSubmit={(e) => {
          void form.handleSubmit(onSubmit)(e);
        }}
      >
        <div className="space-y-2">
          <div className="text-sm font-medium text-foreground">{t("login.email")}</div>
          <div className="relative">
            <Mail
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              placeholder="you@company.com"
              autoComplete="email"
              className={cn(
                "bg-transparent pl-9",
                "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
              )}
              {...form.register("email", {
                validate: (value) => {
                  const r = emailSchema.safeParse(value);
                  return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                }
              })}
            />
          </div>
          {form.formState.errors.email ? (
            <p className="text-xs text-destructive">{form.formState.errors.email.message}</p>
          ) : null}
        </div>

        <div className="space-y-2">
          <div className="text-sm font-medium text-foreground">{t("login.password")}</div>
          <div className="relative">
            <Lock
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              type="password"
              placeholder="••••••••"
              autoComplete="current-password"
              className={cn(
                "bg-transparent pl-9",
                "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
              )}
              {...form.register("password", {
                validate: (value) => {
                  const r = passwordSchema.safeParse(value);
                  return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                }
              })}
            />
          </div>
          {form.formState.errors.password ? (
            <p className="text-xs text-destructive">
              {form.formState.errors.password.message}
            </p>
          ) : null}
        </div>

        <Button
          type="submit"
          className={cn(
            "w-full rounded-xl",
            "transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg",
            "disabled:hover:translate-y-0 disabled:hover:shadow-none"
          )}
          disabled={!form.formState.isValid || loading}
        >
          {loading ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" />
              {t("login.submitting")}
            </>
          ) : (
            t("login.submit")
          )}
        </Button>

        <div className="relative py-2">
          <Separator />
          <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-background px-3 text-xs text-muted-foreground">
            {t("login.or")}
          </div>
        </div>

        <div className="space-y-2">
          <Button
            type="button"
            variant="outline"
            className="w-full rounded-xl bg-transparent"
            onClick={() => toast.message(t("login.githubSoon"))}
          >
            <Github suppressHydrationWarning className="h-4 w-4" />
            {t("login.continueGithub")}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="w-full rounded-xl bg-transparent"
            onClick={() => {
              const next = nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard";
              window.location.href = `/api/auth/google?next=${encodeURIComponent(next)}`;
            }}
          >
            <Chrome suppressHydrationWarning className="h-4 w-4" />
            {t("login.continueGoogle")}
          </Button>
        </div>
      </form>

      <p className="mt-8 text-center text-xs text-muted-foreground">
        {t("auth.termsLine")}
      </p>
    </div>
  );
}
