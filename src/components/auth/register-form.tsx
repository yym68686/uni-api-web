"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { z } from "zod";
import { Chrome, Github, Loader2, Lock, Mail, ShieldCheck } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { BrandWordmark } from "@/components/brand/wordmark";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";

function createCodeSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.string().trim().regex(/^\d{6}$/, t("validation.code6"));
}

function createRegisterSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  const emailSchema = z.string().trim().email(t("validation.email"));
  const passwordSchema = z
    .string()
    .min(6, t("validation.passwordMin", { min: 6 }))
    .max(128, t("validation.passwordMax"));

  return z
    .object({
      email: emailSchema,
      password: passwordSchema,
      confirmPassword: z.string(),
      code: z.string().optional()
    })
    .refine((v) => v.password === v.confirmPassword, {
      message: t("validation.passwordMismatch"),
      path: ["confirmPassword"]
    });
}

type FormValues = z.infer<ReturnType<typeof createRegisterSchema>>;

interface RegisterFormProps {
  appName: string;
  nextPath?: string;
  className?: string;
}

export function RegisterForm({ appName, nextPath, className }: RegisterFormProps) {
  const [loading, setLoading] = React.useState(false);
  const [step, setStep] = React.useState<"details" | "verify">("details");
  const [cooldown, setCooldown] = React.useState(0);
  const router = useRouter();
  const searchParams = useSearchParams();
  const { t } = useI18n();
  const oauthToastShownRef = React.useRef(false);

  const schema = React.useMemo(() => createRegisterSchema(t), [t]);
  const emailSchema = schema.shape.email;
  const passwordSchema = schema.shape.password;
  const codeSchema = React.useMemo(() => createCodeSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: { email: "", password: "", confirmPassword: "", code: "" },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const t = window.setInterval(() => setCooldown((v) => Math.max(0, v - 1)), 1000);
    return () => window.clearInterval(t);
  }, [cooldown]);

  React.useEffect(() => {
    const oauthError = searchParams.get("oauth_error");
    if (!oauthError || oauthToastShownRef.current) return;
    oauthToastShownRef.current = true;
    const message =
      oauthError === "registration_disabled"
        ? t("auth.oauth.registrationDisabled")
        : oauthError === "banned"
          ? t("auth.oauth.banned")
          : t("auth.oauth.failed");
    const id = window.setTimeout(() => toast.error(message), 0);

    const url = new URL(window.location.href);
    url.searchParams.delete("oauth_error");
    window.history.replaceState(null, "", url.toString());
    return () => window.clearTimeout(id);
  }, [searchParams, t]);

  async function requestCode() {
    const values = form.getValues();
    const parsed = schema.safeParse(values);
    if (!parsed.success) {
      const issues = parsed.error.issues;
      for (const issue of issues) {
        const key = issue.path[0];
        if (key === "email") form.setError("email", { message: issue.message, type: "validate" });
        if (key === "password") form.setError("password", { message: issue.message, type: "validate" });
        if (key === "confirmPassword") {
          form.setError("confirmPassword", { message: issue.message, type: "validate" });
        }
      }
      toast.error(issues[0]?.message ?? t("common.formInvalid"));
      return;
    }

    setLoading(true);
    try {
      const res = await fetch("/api/auth/email/request", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email: parsed.data.email, purpose: "register" })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("register.failed"))
            : t("register.failed");
        throw new Error(message);
      }

      toast.success(t("register.codeSent"));
      setStep("verify");
      setCooldown(60);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("register.failed"));
    } finally {
      setLoading(false);
    }
  }

  async function onSubmit(values: FormValues) {
    if (step === "details") {
      await requestCode();
      return;
    }

    setLoading(true);
    try {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        toast.error(parsed.error.issues[0]?.message ?? t("register.failed"));
        return;
      }
      const codeParsed = codeSchema.safeParse(parsed.data.code ?? "");
      if (!codeParsed.success) {
        form.setError("code", { message: codeParsed.error.issues[0]?.message ?? t("register.failed") });
        toast.error(codeParsed.error.issues[0]?.message ?? t("register.failed"));
        return;
      }

      const res = await fetch("/api/auth/register/verify", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          email: parsed.data.email,
          password: parsed.data.password,
          code: codeParsed.data
        })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message =
          json && typeof json === "object" && "message" in json
            ? String((json as { message?: unknown }).message ?? t("register.failed"))
            : t("register.failed");
        throw new Error(message);
      }

      toast.success(t("register.success"));
      const next = nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard";
      router.replace(next);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("register.failed"));
    } finally {
      setLoading(false);
    }
  }

  const linkHref = nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : "/login";

  return (
    <div className={cn("w-full max-w-sm", className)}>
      <div className="text-center">
        <BrandWordmark name={appName} className="text-lg" />
        <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground">
          {t("register.title")}
        </div>
        <p className="mt-2 text-sm text-muted-foreground">
          {t("register.haveAccount")}{" "}
          <Link href={linkHref} className="text-primary hover:underline">
            {t("register.signIn")}
          </Link>
        </p>
      </div>

      <form
        className="mt-8 space-y-4"
        onSubmit={(e) => {
          void form.handleSubmit(onSubmit)(e);
        }}
      >
        <div className={cn("space-y-2", step === "verify" ? "opacity-80" : "")}>
          <div className="text-sm font-medium text-foreground">{t("login.email")}</div>
          <div className="relative">
            <Mail
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              placeholder="you@company.com"
              autoComplete="email"
              disabled={step === "verify"}
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

        <div className={cn("space-y-2", step === "verify" ? "opacity-80" : "")}>
          <div className="text-sm font-medium text-foreground">{t("login.password")}</div>
          <div className="relative">
            <Lock
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              type="password"
              placeholder="••••••••"
              autoComplete="new-password"
              disabled={step === "verify"}
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

        <div className={cn("space-y-2", step === "verify" ? "opacity-80" : "")}>
          <div className="text-sm font-medium text-foreground">{t("register.confirmPassword")}</div>
          <div className="relative">
            <Lock
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              type="password"
              placeholder="••••••••"
              autoComplete="new-password"
              disabled={step === "verify"}
              className={cn(
                "bg-transparent pl-9",
                "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
              )}
              {...form.register("confirmPassword")}
            />
          </div>
          {form.formState.errors.confirmPassword ? (
            <p className="text-xs text-destructive">
              {form.formState.errors.confirmPassword.message}
            </p>
          ) : null}
        </div>

        {step === "verify" ? (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <div className="text-sm font-medium text-foreground">{t("register.code")}</div>
              <Button
                type="button"
                variant="ghost"
                className="h-8 rounded-xl px-3 text-xs"
                disabled={loading || cooldown > 0}
                onClick={() => void requestCode()}
              >
                {cooldown > 0 ? t("register.resendIn", { seconds: cooldown }) : t("register.resend")}
              </Button>
            </div>
            <div className="relative">
              <ShieldCheck
                suppressHydrationWarning
                className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                placeholder="123456"
                inputMode="numeric"
                autoComplete="one-time-code"
                className={cn(
                  "bg-transparent pl-9",
                  "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
                )}
                {...form.register("code")}
              />
            </div>
            {form.formState.errors.code ? (
              <p className="text-xs text-destructive">{form.formState.errors.code.message}</p>
            ) : (
              <p className="text-xs text-muted-foreground">
                {t("register.codeHint")}
              </p>
            )}
            <Button
              type="button"
              variant="ghost"
              className="h-9 w-full rounded-xl"
              disabled={loading}
              onClick={() => {
                setStep("details");
                form.setValue("code", "");
              }}
            >
              {t("register.changeDetails")}
            </Button>
          </div>
        ) : null}

        <Button
          type="submit"
          className={cn(
            "w-full rounded-xl",
            "transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg",
            "disabled:hover:translate-y-0 disabled:hover:shadow-none"
          )}
          disabled={
            loading ||
            (step === "details" ? !form.formState.isValid : !form.getValues().code)
          }
        >
          {loading ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" />
              {step === "details" ? t("register.sending") : t("register.creating")}
            </>
          ) : (
            step === "details" ? t("register.sendCode") : t("register.title")
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
            disabled={step === "verify"}
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
              window.location.href = `/api/auth/google?from=/register&next=${encodeURIComponent(next)}`;
            }}
            disabled={step === "verify"}
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
