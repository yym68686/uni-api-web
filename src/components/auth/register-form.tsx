"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Chrome, Github, Loader2, Lock, Mail, ShieldCheck } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";

const emailSchema = z.string().trim().email("请输入正确的邮箱");
const passwordSchema = z.string().min(6, "至少 6 位密码").max(128, "密码过长");
const codeSchema = z.string().trim().regex(/^\d{6}$/, "请输入 6 位验证码");

const schema = z
  .object({
    email: emailSchema,
    password: passwordSchema,
    confirmPassword: z.string(),
    code: z.string().optional()
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "两次密码不一致",
    path: ["confirmPassword"]
  });

type FormValues = z.infer<typeof schema>;

interface RegisterFormProps {
  nextPath?: string;
  className?: string;
}

export function RegisterForm({ nextPath, className }: RegisterFormProps) {
  const [loading, setLoading] = React.useState(false);
  const [step, setStep] = React.useState<"details" | "verify">("details");
  const [cooldown, setCooldown] = React.useState(0);
  const router = useRouter();

  const form = useForm<FormValues>({
    defaultValues: { email: "", password: "", confirmPassword: "", code: "" },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const t = window.setInterval(() => setCooldown((v) => Math.max(0, v - 1)), 1000);
    return () => window.clearInterval(t);
  }, [cooldown]);

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
      toast.error(issues[0]?.message ?? "表单校验失败");
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
            ? String((json as { message?: unknown }).message ?? "发送失败")
            : "发送失败";
        throw new Error(message);
      }

      toast.success("验证码已发送");
      setStep("verify");
      setCooldown(60);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "发送失败");
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
        toast.error(parsed.error.issues[0]?.message ?? "表单校验失败");
        return;
      }
      const codeParsed = codeSchema.safeParse(parsed.data.code ?? "");
      if (!codeParsed.success) {
        form.setError("code", { message: codeParsed.error.issues[0]?.message ?? "验证码不合法" });
        toast.error(codeParsed.error.issues[0]?.message ?? "验证码不合法");
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
            ? String((json as { message?: unknown }).message ?? "注册失败")
            : "注册失败";
        throw new Error(message);
      }

      toast.success("注册成功");
      const next = nextPath && nextPath.startsWith("/") ? nextPath : "/";
      router.replace(next);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "注册失败");
    } finally {
      setLoading(false);
    }
  }

  const linkHref = nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : "/login";

  return (
    <div className={cn("w-full max-w-sm", className)}>
      <div className="text-center">
        <div className="font-logo text-lg tracking-tight text-foreground">MyApp</div>
        <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground">
          Create account
        </div>
        <p className="mt-2 text-sm text-muted-foreground">
          已有账号？{" "}
          <Link href={linkHref} className="text-primary hover:underline">
            Sign in
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
          <div className="text-sm font-medium text-foreground">Email</div>
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
                  return r.success ? true : (r.error.issues[0]?.message ?? "邮箱不合法");
                }
              })}
            />
          </div>
          {form.formState.errors.email ? (
            <p className="text-xs text-destructive">{form.formState.errors.email.message}</p>
          ) : null}
        </div>

        <div className={cn("space-y-2", step === "verify" ? "opacity-80" : "")}>
          <div className="text-sm font-medium text-foreground">Password</div>
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
                  return r.success ? true : (r.error.issues[0]?.message ?? "密码不合法");
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
          <div className="text-sm font-medium text-foreground">Confirm password</div>
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
              <div className="text-sm font-medium text-foreground">Verification code</div>
              <Button
                type="button"
                variant="ghost"
                className="h-8 rounded-xl px-3 text-xs"
                disabled={loading || cooldown > 0}
                onClick={() => void requestCode()}
              >
                {cooldown > 0 ? `Resend in ${cooldown}s` : "Resend"}
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
                请输入邮箱中的 6 位验证码。
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
              Change email / password
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
              {step === "details" ? "Sending…" : "Creating…"}
            </>
          ) : (
            step === "details" ? "Send code" : "Create account"
          )}
        </Button>

        <div className="relative py-2">
          <Separator />
          <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-background px-3 text-xs text-muted-foreground">
            or
          </div>
        </div>

        <div className="space-y-2">
          <Button
            type="button"
            variant="outline"
            className="w-full rounded-xl bg-transparent"
            onClick={() => toast.message("GitHub 登录：即将支持")}
            disabled={step === "verify"}
          >
            <Github suppressHydrationWarning className="h-4 w-4" />
            Continue with GitHub
          </Button>
          <Button
            type="button"
            variant="outline"
            className="w-full rounded-xl bg-transparent"
            onClick={() => {
              const next = nextPath && nextPath.startsWith("/") ? nextPath : "/";
              window.location.href = `/api/auth/google?next=${encodeURIComponent(next)}`;
            }}
            disabled={step === "verify"}
          >
            <Chrome suppressHydrationWarning className="h-4 w-4" />
            Continue with Google
          </Button>
        </div>
      </form>

      <p className="mt-8 text-center text-xs text-muted-foreground">
        By continuing, you agree to the{" "}
        <span className="text-foreground/80">Terms</span> and{" "}
        <span className="text-foreground/80">Privacy Policy</span>.
      </p>
    </div>
  );
}
