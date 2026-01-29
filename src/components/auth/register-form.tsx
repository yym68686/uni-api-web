"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Chrome, Github, Loader2, Lock, Mail, ShieldCheck, Tag } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { BrandWordmark } from "@/components/brand/wordmark";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import { ensureDeviceIdCookie } from "@/lib/device-id";
import { writeLoginPrefill } from "@/lib/login-prefill";

function formatUpstreamMessage(
  json: unknown,
  fallback: string,
  t: (key: MessageKey, vars?: MessageVars) => string
) {
  if (!json || typeof json !== "object") return fallback;
  if (!("message" in json) || typeof (json as { message?: unknown }).message !== "string") return fallback;

  const message = String((json as { message?: string }).message ?? "").trim();
  if (message === "") return fallback;

  if (message === "registration disabled") return t("auth.oauth.registrationDisabled");
  if (message === "email already registered") return t("profile.security.emailAlreadyRegistered");
  if (message === "invalid invite code") return t("register.inviteCodeInvalid");
  if (message === "invalid code") return t("profile.security.invalidCode");
  if (message === "too many requests") return t("profile.security.tooManyRequests");
  if (message === "please wait") return t("profile.security.pleaseWait");

  return message;
}

function createCodeSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.string().trim().regex(/^\d{6}$/, t("validation.code6"));
}

function createRegisterSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  const emailSchema = z.string().trim().email(t("validation.email"));
  const passwordSchema = z
    .string()
    .min(6, t("validation.passwordMin", { min: 6 }))
    .max(128, t("validation.passwordMax"));
  const inviteCodeSchema = z
    .string()
    .trim()
    .max(16, t("validation.maxChars", { max: 16 }))
    .regex(/^[a-zA-Z0-9]*$/, t("validation.inviteCode"));

  return z
    .object({
      email: emailSchema,
      password: passwordSchema,
      confirmPassword: z.string(),
      inviteCode: inviteCodeSchema.optional(),
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
  defaultInviteCode?: string;
  className?: string;
}

export function RegisterForm({ appName, nextPath, defaultInviteCode, className }: RegisterFormProps) {
  const [sendingCode, setSendingCode] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [step, setStep] = React.useState<"details" | "verify">("details");
  const [cooldown, setCooldown] = React.useState(0);
  const router = useRouter();
  const { t } = useI18n();
  const oauthToastShownRef = React.useRef(false);

  const schema = React.useMemo(() => createRegisterSchema(t), [t]);
  const emailSchema = schema.shape.email;
  const passwordSchema = schema.shape.password;
  const inviteCodeSchema = schema.shape.inviteCode;
  const codeSchema = React.useMemo(() => createCodeSchema(t), [t]);

  const form = useForm<FormValues>({
    defaultValues: {
      email: "",
      password: "",
      confirmPassword: "",
      inviteCode: defaultInviteCode ?? "",
      code: ""
    },
    mode: "onChange"
  });

  const [passwordValue, confirmPasswordValue, codeValue] = form.watch(["password", "confirmPassword", "code"]);
  const canCreate =
    form.formState.isValid &&
    passwordValue === confirmPasswordValue &&
    /^\d{6}$/.test(String(codeValue ?? "").trim());

  React.useEffect(() => {
    ensureDeviceIdCookie();
  }, []);

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const t = window.setInterval(() => setCooldown((v) => Math.max(0, v - 1)), 1000);
    return () => window.clearInterval(t);
  }, [cooldown]);

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
          : oauthError === "invalid_invite_code"
            ? t("register.inviteCodeInvalid")
          : t("auth.oauth.failed");
    const id = window.setTimeout(() => toast.error(message), 0);

    const url = new URL(window.location.href);
    url.searchParams.delete("oauth_error");
    window.history.replaceState(null, "", url.toString());
    return () => window.clearTimeout(id);
  }, [t]);

  async function requestCode() {
    const values = form.getValues();
    const parsed = schema.safeParse(values);
    if (!parsed.success) {
      const issues = parsed.error.issues;
      for (const issue of issues) {
        const key = issue.path[0];
        if (key === "email") form.setError("email", { message: issue.message, type: "validate" });
        if (key === "password") form.setError("password", { message: issue.message, type: "validate" });
        if (key === "inviteCode") form.setError("inviteCode", { message: issue.message, type: "validate" });
        if (key === "confirmPassword") {
          form.setError("confirmPassword", { message: issue.message, type: "validate" });
        }
      }
      toast.error(issues[0]?.message ?? t("common.formInvalid"));
      return;
    }

    setSendingCode(true);
    try {
      const res = await fetch("/api/auth/email/request", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email: parsed.data.email, purpose: "register" })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const message = formatUpstreamMessage(json, t("register.failed"), t);
        throw new Error(message);
      }

      toast.success(t("register.codeSent"));
      setStep("verify");
      setCooldown(60);
      form.setValue("code", "");
      form.setFocus("code");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("register.failed"));
    } finally {
      setSendingCode(false);
    }
  }

  async function onSubmit(values: FormValues) {
    setCreating(true);
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
          code: codeParsed.data,
          inviteCode: (parsed.data.inviteCode ?? "").trim() || undefined
        })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const rawMessage =
          json && typeof json === "object" && "message" in json ? String((json as { message?: unknown }).message) : "";
        const normalized = rawMessage.trim().toLowerCase();
        const message = formatUpstreamMessage(json, t("register.failed"), t);

        if (normalized === "email already registered") {
          toast.error(message, {
            action: {
              label: t("register.signIn"),
              onClick: () => {
                writeLoginPrefill({ email: parsed.data.email, password: parsed.data.password });
                const href = nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : "/login";
                router.push(href);
              }
            }
          });
          return;
        }

        throw new Error(message);
      }

      toast.success(t("register.success"));
      const next = nextPath && nextPath.startsWith("/") ? nextPath : "/dashboard";
      router.replace(next);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("register.failed"));
    } finally {
      setCreating(false);
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
          <Link
            href={linkHref}
            className="text-primary hover:underline"
            onClick={() => {
              const { email, password } = form.getValues();
              const safeEmail = String(email ?? "").trim();
              const safePassword = String(password ?? "");
              if (safeEmail && safePassword) {
                writeLoginPrefill({ email: safeEmail, password: safePassword });
              }
            }}
          >
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
              autoComplete="new-password"
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

        <div className="space-y-2">
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
              className={cn(
                "bg-transparent pl-9",
                "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
              )}
              {...form.register("confirmPassword", {
                validate: (value) => {
                  const password = form.getValues().password;
                  return value === password ? true : t("validation.passwordMismatch");
                }
              })}
            />
          </div>
          {form.formState.errors.confirmPassword ? (
            <p className="text-xs text-destructive">
              {form.formState.errors.confirmPassword.message}
            </p>
          ) : null}
        </div>

        <div className="space-y-2">
          <div className="text-sm font-medium text-foreground">{t("register.code")}</div>
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
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
            <Button
              type="button"
              variant="outline"
              className="h-10 shrink-0 rounded-xl bg-transparent"
              disabled={sendingCode || cooldown > 0}
              onClick={() => void requestCode()}
            >
              {sendingCode ? (
                <span className="inline-flex animate-spin">
                  <Loader2 className="h-4 w-4" />
                </span>
              ) : null}
              {cooldown > 0
                ? t("register.resendIn", { seconds: cooldown })
                : step === "verify"
                  ? t("register.resend")
                  : t("register.sendCode")}
            </Button>
          </div>
          {form.formState.errors.code ? (
            <p className="text-xs text-destructive">{form.formState.errors.code.message}</p>
          ) : null}
        </div>

        <div className="space-y-2">
          <div className="text-sm font-medium text-foreground">{t("register.inviteCode")}</div>
          <div className="relative">
            <Tag
              suppressHydrationWarning
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              placeholder={t("register.inviteCodePlaceholder")}
              autoComplete="off"
              className={cn(
                "bg-transparent pl-9",
                "border-border/60 focus-visible:ring-primary focus-visible:border-primary/40"
              )}
              {...form.register("inviteCode", {
                validate: (value) => {
                  const r = inviteCodeSchema.safeParse(value);
                  return r.success ? true : (r.error.issues[0]?.message ?? t("common.formInvalid"));
                }
              })}
            />
          </div>
          {form.formState.errors.inviteCode ? (
            <p className="text-xs text-destructive">{form.formState.errors.inviteCode.message}</p>
          ) : null}
        </div>

        <Button
          type="submit"
          className={cn(
            "w-full rounded-xl",
            "transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg",
            "disabled:hover:translate-y-0 disabled:hover:shadow-none"
          )}
          disabled={creating || !canCreate}
      >
        {creating ? (
          <>
            <span className="inline-flex animate-spin">
              <Loader2 className="h-4 w-4" />
            </span>
            {t("register.creating")}
          </>
        ) : (
          t("register.title")
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
              const inviteCode = (form.getValues().inviteCode ?? "").trim();
              const refParam = inviteCode ? `&ref=${encodeURIComponent(inviteCode)}` : "";
              window.location.href = `/api/auth/google?from=/register&next=${encodeURIComponent(next)}${refParam}`;
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
