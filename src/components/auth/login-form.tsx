"use client";

import * as React from "react";
import Link from "next/link";
import { z } from "zod";
import { Chrome, Github, Loader2, Lock, Mail, ShieldCheck } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { clearSwrLite } from "@/lib/swr-lite";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { BrandWordmark } from "@/components/brand/wordmark";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import { readLoginPrefill } from "@/lib/login-prefill";

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

function createResetPasswordSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  const emailSchema = z.string().trim().email(t("validation.email"));
  const passwordSchema = z
    .string()
    .min(6, t("validation.passwordMin", { min: 6 }))
    .max(128, t("validation.passwordMax"));
  return z
    .object({
      email: emailSchema,
      code: z.string().trim().regex(/^\d{6}$/, t("validation.code6")),
      password: passwordSchema,
      confirmPassword: z.string()
    })
    .refine((v) => v.password === v.confirmPassword, {
      message: t("validation.passwordMismatch"),
      path: ["confirmPassword"]
    });
}

type FormValues = z.infer<ReturnType<typeof createLoginSchema>>;
type ResetPasswordValues = z.infer<ReturnType<typeof createResetPasswordSchema>>;

interface LoginFormProps {
  appName: string;
  nextPath?: string;
  className?: string;
}

export function LoginForm({ appName, nextPath, className }: LoginFormProps) {
  const [loading, setLoading] = React.useState(false);
  const [resetOpen, setResetOpen] = React.useState(false);
  const [sendingResetCode, setSendingResetCode] = React.useState(false);
  const [resettingPassword, setResettingPassword] = React.useState(false);
  const [resetCooldown, setResetCooldown] = React.useState(0);
  const { t } = useI18n();
  const oauthToastShownRef = React.useRef(false);

  const schema = React.useMemo(() => createLoginSchema(t), [t]);
  const emailSchema = schema.shape.email;
  const passwordSchema = schema.shape.password;
  const resetSchema = React.useMemo(() => createResetPasswordSchema(t), [t]);
  const resetEmailSchema = resetSchema.shape.email;
  const resetCodeSchema = resetSchema.shape.code;
  const resetPasswordSchema = resetSchema.shape.password;

  const form = useForm<FormValues>({
    defaultValues: { email: "", password: "" },
    mode: "onChange"
  });

  const resetForm = useForm<ResetPasswordValues>({
    defaultValues: { email: "", code: "", password: "", confirmPassword: "" },
    mode: "onChange"
  });

  const [resetEmailValue, resetCodeValue, resetPasswordValue, resetConfirmPasswordValue] = resetForm.watch([
    "email",
    "code",
    "password",
    "confirmPassword"
  ]);
  const canResetPassword =
    resetEmailSchema.safeParse(resetEmailValue).success &&
    resetPasswordSchema.safeParse(resetPasswordValue).success &&
    resetPasswordValue === resetConfirmPasswordValue &&
    /^\d{6}$/.test(String(resetCodeValue ?? "").trim());

  React.useEffect(() => {
    const prefill = readLoginPrefill();
    if (!prefill) return;

    form.setValue("email", prefill.email, { shouldDirty: true, shouldValidate: true });
    form.setValue("password", prefill.password, { shouldDirty: true, shouldValidate: true });
  }, [form]);

  React.useEffect(() => {
    if (oauthToastShownRef.current) return;
    const oauthError = new URLSearchParams(window.location.search).get("oauth_error");
    if (!oauthError) return;
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
  }, [t]);

  React.useEffect(() => {
    if (resetCooldown <= 0) return;
    const id = window.setInterval(() => setResetCooldown((v) => Math.max(0, v - 1)), 1000);
    return () => window.clearInterval(id);
  }, [resetCooldown]);

  function resolveLoginErrorMessage(payload: unknown) {
    if (!payload || typeof payload !== "object") return t("login.failed");
    const obj = payload as { code?: unknown; message?: unknown };
    const code = typeof obj.code === "string" ? obj.code : null;
    const message = typeof obj.message === "string" ? obj.message : null;

    if (code === "invalid_credentials") return t("login.invalidCredentials");
    if (message && /invalid credentials/i.test(message)) return t("login.invalidCredentials");

    return message ?? t("login.failed");
  }

  function resolvePasswordResetMessage(payload: unknown, fallback: string) {
    if (!payload || typeof payload !== "object") return fallback;
    const message =
      "message" in payload && typeof (payload as { message?: unknown }).message === "string"
        ? String((payload as { message?: string }).message ?? "")
        : "";
    if (message === "invalid code") return t("profile.security.invalidCode");
    if (message === "please wait") return t("profile.security.pleaseWait");
    if (message === "too many requests") return t("profile.security.tooManyRequests");
    if (message === "Invalid payload" || message === "invalid payload") return t("common.formInvalid");
    return message || fallback;
  }

  function getExpiresInSeconds(payload: unknown) {
    if (!payload || typeof payload !== "object" || !("expiresInSeconds" in payload)) return 60;
    const raw = (payload as { expiresInSeconds?: unknown }).expiresInSeconds;
    const value = typeof raw === "number" ? raw : Number(raw);
    return Number.isFinite(value) ? value : 60;
  }

  function applyResetValidationErrors(error: z.ZodError<ResetPasswordValues>) {
    for (const issue of error.issues) {
      const key = issue.path[0];
      if (key === "email") resetForm.setError("email", { message: issue.message, type: "validate" });
      if (key === "code") resetForm.setError("code", { message: issue.message, type: "validate" });
      if (key === "password") resetForm.setError("password", { message: issue.message, type: "validate" });
      if (key === "confirmPassword") {
        resetForm.setError("confirmPassword", { message: issue.message, type: "validate" });
      }
    }
  }

  function openResetDialog() {
    const email = form.getValues("email").trim();
    resetForm.reset({ email, code: "", password: "", confirmPassword: "" });
    setResetCooldown(0);
    setResetOpen(true);
  }

  function handleResetOpenChange(open: boolean) {
    setResetOpen(open);
    if (!open) {
      setSendingResetCode(false);
      setResettingPassword(false);
      setResetCooldown(0);
      resetForm.reset({ email: "", code: "", password: "", confirmPassword: "" });
    }
  }

  async function requestResetCode() {
    if (resetCooldown > 0 || sendingResetCode || resettingPassword) return;

    const emailParsed = resetEmailSchema.safeParse(resetForm.getValues("email"));
    if (!emailParsed.success) {
      const message = emailParsed.error.issues[0]?.message ?? t("common.formInvalid");
      resetForm.setError("email", { message, type: "validate" });
      toast.error(message);
      return;
    }

    setSendingResetCode(true);
    try {
      const res = await fetch("/api/auth/password/reset/request-code", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email: emailParsed.data })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(resolvePasswordResetMessage(json, t("login.resetCodeFailed")));

      toast.success(t("login.resetCodeSent"));
      setResetCooldown(Math.max(30, Math.min(120, getExpiresInSeconds(json))));
      resetForm.setFocus("code");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("login.resetCodeFailed"));
    } finally {
      setSendingResetCode(false);
    }
  }

  async function onResetSubmit(values: ResetPasswordValues) {
    setResettingPassword(true);
    try {
      const parsed = resetSchema.safeParse(values);
      if (!parsed.success) {
        applyResetValidationErrors(parsed.error);
        toast.error(parsed.error.issues[0]?.message ?? t("login.resetFailed"));
        return;
      }

      const res = await fetch("/api/auth/password/reset", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          email: parsed.data.email,
          code: parsed.data.code,
          password: parsed.data.password
        })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(resolvePasswordResetMessage(json, t("login.resetFailed")));

      toast.success(t("login.resetSuccess"));
      form.setValue("email", parsed.data.email, { shouldDirty: true, shouldValidate: true });
      form.setValue("password", "", { shouldDirty: false, shouldValidate: true });
      handleResetOpenChange(false);
      window.setTimeout(() => form.setFocus("password"), 0);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("login.resetFailed"));
    } finally {
      setResettingPassword(false);
    }
  }

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
        throw new Error(resolveLoginErrorMessage(json));
      }

      toast.success(t("login.success"));
      const next = nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard";
      clearSwrLite();
      window.location.replace(next);
      return;
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
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium text-foreground">{t("login.password")}</div>
            <button
              type="button"
              className="text-xs font-medium text-primary transition-colors hover:text-primary/80 hover:underline"
              onClick={openResetDialog}
            >
              {t("login.forgotPassword")}
            </button>
          </div>
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
              <span className="inline-flex animate-spin">
                <Loader2 className="h-4 w-4" />
              </span>
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
              window.location.href = `/api/auth/google?from=/login&next=${encodeURIComponent(next)}`;
            }}
          >
            <Chrome suppressHydrationWarning className="h-4 w-4" />
            {t("login.continueGoogle")}
          </Button>
        </div>
      </form>

      <Dialog open={resetOpen} onOpenChange={handleResetOpenChange}>
        <DialogContent className="max-w-md rounded-2xl border-border bg-card/95 shadow-[0_0_0_1px_oklch(var(--border)/0.6),0_14px_40px_oklch(0%_0_0/0.35)] backdrop-blur-xl">
          <DialogHeader>
            <div className="mb-1 flex h-10 w-10 items-center justify-center rounded-xl border border-border bg-primary/10 text-primary">
              <ShieldCheck className="h-5 w-5" />
            </div>
            <DialogTitle>{t("login.resetTitle")}</DialogTitle>
            <DialogDescription>{t("login.resetDesc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void resetForm.handleSubmit(onResetSubmit)(e);
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
                  {...resetForm.register("email", {
                    validate: (value) => {
                      const r = resetEmailSchema.safeParse(value);
                      return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                    }
                  })}
                />
              </div>
              {resetForm.formState.errors.email ? (
                <p className="text-xs text-destructive">{resetForm.formState.errors.email.message}</p>
              ) : null}
            </div>

            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3">
              <div className="space-y-2">
                <div className="text-sm font-medium text-foreground">{t("login.resetCode")}</div>
                <Input
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder="000000"
                  className={cn(
                    "bg-transparent font-mono",
                    "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
                  )}
                  {...resetForm.register("code", {
                    validate: (value) => {
                      const r = resetCodeSchema.safeParse(value);
                      return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                    }
                  })}
                />
                {resetForm.formState.errors.code ? (
                  <p className="text-xs text-destructive">{resetForm.formState.errors.code.message}</p>
                ) : null}
              </div>
              <Button
                type="button"
                variant="outline"
                className="mt-7 min-w-24 rounded-xl bg-transparent sm:min-w-28"
                disabled={sendingResetCode || resettingPassword || resetCooldown > 0}
                onClick={() => {
                  void requestResetCode();
                }}
              >
                {sendingResetCode ? (
                  <>
                    <span className="inline-flex animate-spin">
                      <Loader2 className="h-4 w-4" />
                    </span>
                    {t("login.resetSending")}
                  </>
                ) : resetCooldown > 0 ? (
                  t("login.resetResendIn", { seconds: resetCooldown })
                ) : (
                  t("login.resetSendCode")
                )}
              </Button>
            </div>

            <div className="space-y-2">
              <div className="text-sm font-medium text-foreground">{t("login.resetNewPassword")}</div>
              <div className="relative">
                <Lock
                  suppressHydrationWarning
                  className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  type="password"
                  autoComplete="new-password"
                  placeholder="••••••••"
                  className={cn(
                    "bg-transparent pl-9",
                    "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
                  )}
                  {...resetForm.register("password", {
                    validate: (value) => {
                      const r = resetPasswordSchema.safeParse(value);
                      return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                    }
                  })}
                />
              </div>
              {resetForm.formState.errors.password ? (
                <p className="text-xs text-destructive">{resetForm.formState.errors.password.message}</p>
              ) : null}
            </div>

            <div className="space-y-2">
              <div className="text-sm font-medium text-foreground">{t("login.resetConfirmPassword")}</div>
              <div className="relative">
                <Lock
                  suppressHydrationWarning
                  className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  type="password"
                  autoComplete="new-password"
                  placeholder="••••••••"
                  className={cn(
                    "bg-transparent pl-9",
                    "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
                  )}
                  {...resetForm.register("confirmPassword", {
                    validate: (value) => {
                      if (value !== resetForm.getValues("password")) return t("validation.passwordMismatch");
                      return true;
                    }
                  })}
                />
              </div>
              {resetForm.formState.errors.confirmPassword ? (
                <p className="text-xs text-destructive">
                  {resetForm.formState.errors.confirmPassword.message}
                </p>
              ) : null}
            </div>

            <DialogFooter className="pt-2">
              <Button
                type="button"
                variant="outline"
                className="rounded-xl bg-transparent"
                disabled={resettingPassword}
                onClick={() => handleResetOpenChange(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                type="submit"
                className="rounded-xl"
                disabled={!canResetPassword || resettingPassword || sendingResetCode}
              >
                {resettingPassword ? (
                  <>
                    <span className="inline-flex animate-spin">
                      <Loader2 className="h-4 w-4" />
                    </span>
                    {t("login.resetting")}
                  </>
                ) : (
                  t("login.resetSubmit")
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <p className="mt-8 text-center text-xs text-muted-foreground">
        {t("auth.termsLine")}
      </p>
    </div>
  );
}
