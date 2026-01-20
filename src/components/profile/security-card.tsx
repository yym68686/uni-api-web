"use client";

import * as React from "react";
import { z } from "zod";
import { Chrome, KeyRound, Loader2, Lock, ShieldCheck } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import { useI18n } from "@/components/i18n/i18n-provider";
import { Badge } from "@/components/ui/badge";
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
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { AuthMethodsResponse, PasswordRequestCodeResponse } from "@/lib/types";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";

type DialogMode = "closed" | "set" | "change" | "remove";

function createCodeSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.string().trim().regex(/^\d{6}$/, t("validation.code6"));
}

function createPasswordSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z
    .string()
    .min(6, t("validation.passwordMin", { min: 6 }))
    .max(128, t("validation.passwordMax"));
}

function formatUpstreamMessage(
  json: unknown,
  fallback: string,
  t: (key: MessageKey, vars?: MessageVars) => string
) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message === "invalid current password") return t("profile.security.invalidCurrentPassword");
    if (message === "invalid code") return t("profile.security.invalidCode");
    if (message === "please wait") return t("profile.security.pleaseWait");
    if (message === "too many requests") return t("profile.security.tooManyRequests");
    if (message === "no oauth identity") return t("profile.security.noOauth");
    if (message) return message;
  }
  return fallback;
}

function hasGoogle(methods: AuthMethodsResponse | null) {
  return Boolean(methods?.oauth?.some((p) => p.provider === "google"));
}

interface SetPasswordValues {
  code: string;
  password: string;
  confirmPassword: string;
}

interface ChangePasswordValues {
  currentPassword: string;
  newPassword: string;
  confirmPassword: string;
}

interface RemovePasswordValues {
  code: string;
}

interface SecurityCardProps {
  email: string;
  initialMethods: AuthMethodsResponse | null;
  className?: string;
}

export function SecurityCard({ email, initialMethods, className }: SecurityCardProps) {
  const { t } = useI18n();
  const [methods, setMethods] = React.useState<AuthMethodsResponse | null>(initialMethods);
  const [mode, setMode] = React.useState<DialogMode>("closed");
  const [submitting, setSubmitting] = React.useState(false);
  const [cooldown, setCooldown] = React.useState(0);

  const codeSchema = React.useMemo(() => createCodeSchema(t), [t]);
  const passwordSchema = React.useMemo(() => createPasswordSchema(t), [t]);

  const setForm = useForm<SetPasswordValues>({
    defaultValues: { code: "", password: "", confirmPassword: "" },
    mode: "onChange"
  });

  const changeForm = useForm<ChangePasswordValues>({
    defaultValues: { currentPassword: "", newPassword: "", confirmPassword: "" },
    mode: "onChange"
  });

  const removeForm = useForm<RemovePasswordValues>({
    defaultValues: { code: "" },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (cooldown <= 0) return;
    const id = window.setInterval(() => setCooldown((v) => Math.max(0, v - 1)), 1000);
    return () => window.clearInterval(id);
  }, [cooldown]);

  React.useEffect(() => {
    if (methods !== null) return;
    fetch("/api/auth/methods", { cache: "no-store" })
      .then((res) => (res.ok ? res.json() : null))
      .then((json: unknown) => {
        if (!json || typeof json !== "object") return;
        if (!("passwordSet" in json) || !("oauth" in json)) return;
        setMethods(json as AuthMethodsResponse);
      })
      .catch(() => null);
  }, [methods]);

  function close() {
    setMode("closed");
    setSubmitting(false);
    setCooldown(0);
    setForm.reset();
    changeForm.reset();
    removeForm.reset();
  }

  async function requestCode() {
    if (cooldown > 0 || submitting) return;
    setSubmitting(true);
    try {
      const res = await fetch("/api/auth/password/request-code", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: "{}"
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(formatUpstreamMessage(json, t("profile.security.codeFailed"), t));

      const payload = json as PasswordRequestCodeResponse;
      toast.success(t("profile.security.codeSent"));
      setCooldown(Math.max(30, Math.min(120, Number(payload.expiresInSeconds) || 60)));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("profile.security.codeFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitSet(values: SetPasswordValues) {
    setSubmitting(true);
    try {
      const codeParsed = codeSchema.safeParse(values.code);
      if (!codeParsed.success) {
        const message = codeParsed.error.issues[0]?.message ?? t("common.formInvalid");
        setForm.setError("code", { type: "validate", message });
        toast.error(message);
        return;
      }
      const passwordParsed = passwordSchema.safeParse(values.password);
      if (!passwordParsed.success) {
        const message = passwordParsed.error.issues[0]?.message ?? t("common.formInvalid");
        setForm.setError("password", { type: "validate", message });
        toast.error(message);
        return;
      }
      if (values.password !== values.confirmPassword) {
        setForm.setError("confirmPassword", { type: "validate", message: t("validation.passwordMismatch") });
        toast.error(t("validation.passwordMismatch"));
        return;
      }

      const res = await fetch("/api/auth/password/set", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ code: codeParsed.data, password: passwordParsed.data })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(formatUpstreamMessage(json, t("profile.security.setFailed"), t));

      setMethods(json as AuthMethodsResponse);
      toast.success(t("profile.security.setSuccess"));
      close();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("profile.security.setFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitChange(values: ChangePasswordValues) {
    setSubmitting(true);
    try {
      const newPasswordParsed = passwordSchema.safeParse(values.newPassword);
      if (!newPasswordParsed.success) {
        const message = newPasswordParsed.error.issues[0]?.message ?? t("common.formInvalid");
        changeForm.setError("newPassword", { type: "validate", message });
        toast.error(message);
        return;
      }
      if (values.newPassword !== values.confirmPassword) {
        changeForm.setError("confirmPassword", { type: "validate", message: t("validation.passwordMismatch") });
        toast.error(t("validation.passwordMismatch"));
        return;
      }

      const res = await fetch("/api/auth/password/change", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ currentPassword: values.currentPassword, newPassword: newPasswordParsed.data })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(formatUpstreamMessage(json, t("profile.security.changeFailed"), t));

      setMethods(json as AuthMethodsResponse);
      toast.success(t("profile.security.changeSuccess"));
      close();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("profile.security.changeFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitRemove(values: RemovePasswordValues) {
    setSubmitting(true);
    try {
      const codeParsed = codeSchema.safeParse(values.code);
      if (!codeParsed.success) {
        const message = codeParsed.error.issues[0]?.message ?? t("common.formInvalid");
        removeForm.setError("code", { type: "validate", message });
        toast.error(message);
        return;
      }

      const res = await fetch("/api/auth/password/remove", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ code: codeParsed.data })
      });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(formatUpstreamMessage(json, t("profile.security.removeFailed"), t));

      setMethods(json as AuthMethodsResponse);
      toast.success(t("profile.security.removeSuccess"));
      close();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("profile.security.removeFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  const passwordSet = Boolean(methods?.passwordSet);
  const googleConnected = hasGoogle(methods);

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck className="h-5 w-5 text-muted-foreground" />
          {t("profile.security.title")}
        </CardTitle>
        <CardDescription>{t("profile.security.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="rounded-xl border border-border bg-background/35 p-4">
          <div className="flex items-center justify-between gap-4">
            <div className="flex min-w-0 items-center gap-3">
              <div className="flex h-9 w-9 items-center justify-center rounded-xl border border-border bg-muted/20">
                <Chrome className="h-4 w-4 text-muted-foreground" />
              </div>
              <div className="min-w-0">
                <div className="truncate text-sm font-medium text-foreground">{t("profile.security.google")}</div>
                <div className="truncate text-xs text-muted-foreground">{email}</div>
              </div>
            </div>
            <Badge variant={googleConnected ? "success" : "outline"}>
              {googleConnected ? t("profile.security.connected") : t("profile.security.notConnected")}
            </Badge>
          </div>
        </div>

        <div className="rounded-xl border border-border bg-background/35 p-4">
          <div className="flex items-center justify-between gap-4">
            <div className="flex min-w-0 items-center gap-3">
              <div className="flex h-9 w-9 items-center justify-center rounded-xl border border-border bg-muted/20">
                {passwordSet ? (
                  <KeyRound className="h-4 w-4 text-muted-foreground" />
                ) : (
                  <Lock className="h-4 w-4 text-muted-foreground" />
                )}
              </div>
              <div className="min-w-0">
                <div className="truncate text-sm font-medium text-foreground">{t("profile.security.password")}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {passwordSet ? t("profile.security.passwordSet") : t("profile.security.passwordNotSet")}
                </div>
              </div>
            </div>

            <div className="flex items-center gap-2">
              {!passwordSet ? (
                <Button
                  type="button"
                  className="rounded-xl"
                  onClick={() => setMode("set")}
                >
                  {t("profile.security.action.set")}
                </Button>
              ) : (
                <>
                  <Button
                    type="button"
                    variant="outline"
                    className="rounded-xl"
                    onClick={() => setMode("change")}
                  >
                    {t("profile.security.action.change")}
                  </Button>
                  <Button
                    type="button"
                    variant="destructive"
                    className="rounded-xl"
                    disabled={!googleConnected}
                    onClick={() => setMode("remove")}
                  >
                    {t("profile.security.action.remove")}
                  </Button>
                </>
              )}
            </div>
          </div>
          {!googleConnected && passwordSet ? (
            <p className="mt-3 text-xs text-muted-foreground">
              {t("profile.security.removeRequiresOauth")}
            </p>
          ) : null}
        </div>
      </CardContent>

      <Dialog
        open={mode !== "closed"}
        onOpenChange={(open) => {
          if (!open) close();
        }}
      >
        <DialogContent>
          {mode === "set" ? (
            <>
              <DialogHeader>
                <DialogTitle>{t("profile.security.setTitle")}</DialogTitle>
                <DialogDescription>{t("profile.security.setDesc", { email })}</DialogDescription>
              </DialogHeader>

              <form
                className="space-y-4"
                onSubmit={(e) => {
                  void setForm.handleSubmit(submitSet)(e);
                }}
              >
                <div className="flex items-end gap-2">
                  <div className="flex-1 space-y-2">
                    <Label htmlFor="pw-code">{t("profile.security.code")}</Label>
                    <Input
                      id="pw-code"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      {...setForm.register("code")}
                    />
                    {setForm.formState.errors.code ? (
                      <p className="text-xs text-destructive">{setForm.formState.errors.code.message}</p>
                    ) : null}
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="rounded-xl"
                    disabled={cooldown > 0 || submitting}
                    onClick={() => void requestCode()}
                  >
                    {cooldown > 0
                      ? t("profile.security.resendIn", { seconds: String(cooldown) })
                      : t("profile.security.sendCode")}
                  </Button>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="pw-new">{t("profile.security.newPassword")}</Label>
                  <Input id="pw-new" type="password" autoComplete="new-password" {...setForm.register("password")} />
                  {setForm.formState.errors.password ? (
                    <p className="text-xs text-destructive">{setForm.formState.errors.password.message}</p>
                  ) : null}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="pw-confirm">{t("profile.security.confirmPassword")}</Label>
                  <Input id="pw-confirm" type="password" autoComplete="new-password" {...setForm.register("confirmPassword")} />
                  {setForm.formState.errors.confirmPassword ? (
                    <p className="text-xs text-destructive">{setForm.formState.errors.confirmPassword.message}</p>
                  ) : null}
                </div>

                <DialogFooter>
                  <Button type="button" variant="ghost" disabled={submitting} onClick={close}>
                    {t("common.cancel")}
                  </Button>
                  <Button type="submit" className="rounded-xl" disabled={!setForm.formState.isValid || submitting}>
                    {submitting ? (
                      <>
                        <span className="inline-flex animate-spin">
                          <Loader2 className="h-4 w-4" />
                        </span>
                        {t("common.saving")}
                      </>
                    ) : (
                      t("profile.security.action.set")
                    )}
                  </Button>
                </DialogFooter>
              </form>
            </>
          ) : null}

          {mode === "change" ? (
            <>
              <DialogHeader>
                <DialogTitle>{t("profile.security.changeTitle")}</DialogTitle>
                <DialogDescription>{t("profile.security.changeDesc")}</DialogDescription>
              </DialogHeader>

              <form
                className="space-y-4"
                onSubmit={(e) => {
                  void changeForm.handleSubmit(submitChange)(e);
                }}
              >
                <div className="space-y-2">
                  <Label htmlFor="pw-current">{t("profile.security.currentPassword")}</Label>
                  <Input
                    id="pw-current"
                    type="password"
                    autoComplete="current-password"
                    {...changeForm.register("currentPassword")}
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="pw-next">{t("profile.security.newPassword")}</Label>
                  <Input id="pw-next" type="password" autoComplete="new-password" {...changeForm.register("newPassword")} />
                  {changeForm.formState.errors.newPassword ? (
                    <p className="text-xs text-destructive">{changeForm.formState.errors.newPassword.message}</p>
                  ) : null}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="pw-next-confirm">{t("profile.security.confirmPassword")}</Label>
                  <Input id="pw-next-confirm" type="password" autoComplete="new-password" {...changeForm.register("confirmPassword")} />
                  {changeForm.formState.errors.confirmPassword ? (
                    <p className="text-xs text-destructive">{changeForm.formState.errors.confirmPassword.message}</p>
                  ) : null}
                </div>

                <DialogFooter>
                  <Button type="button" variant="ghost" disabled={submitting} onClick={close}>
                    {t("common.cancel")}
                  </Button>
                  <Button type="submit" className="rounded-xl" disabled={!changeForm.formState.isValid || submitting}>
                    {submitting ? (
                      <>
                        <span className="inline-flex animate-spin">
                          <Loader2 className="h-4 w-4" />
                        </span>
                        {t("common.saving")}
                      </>
                    ) : (
                      t("profile.security.action.change")
                    )}
                  </Button>
                </DialogFooter>
              </form>
            </>
          ) : null}

          {mode === "remove" ? (
            <>
              <DialogHeader>
                <DialogTitle>{t("profile.security.removeTitle")}</DialogTitle>
                <DialogDescription>{t("profile.security.removeDesc")}</DialogDescription>
              </DialogHeader>

              <form
                className="space-y-4"
                onSubmit={(e) => {
                  void removeForm.handleSubmit(submitRemove)(e);
                }}
              >
                <div className="flex items-end gap-2">
                  <div className="flex-1 space-y-2">
                    <Label htmlFor="pw-remove-code">{t("profile.security.code")}</Label>
                    <Input
                      id="pw-remove-code"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      {...removeForm.register("code")}
                    />
                    {removeForm.formState.errors.code ? (
                      <p className="text-xs text-destructive">{removeForm.formState.errors.code.message}</p>
                    ) : null}
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="rounded-xl"
                    disabled={cooldown > 0 || submitting}
                    onClick={() => void requestCode()}
                  >
                    {cooldown > 0
                      ? t("profile.security.resendIn", { seconds: String(cooldown) })
                      : t("profile.security.sendCode")}
                  </Button>
                </div>

                <div className={cn("rounded-xl border border-border bg-destructive/5 p-4 text-xs text-muted-foreground")}>
                  {t("profile.security.removeWarn")}
                </div>

                <DialogFooter>
                  <Button type="button" variant="ghost" disabled={submitting} onClick={close}>
                    {t("common.cancel")}
                  </Button>
                  <Button type="submit" variant="destructive" className="rounded-xl" disabled={!removeForm.formState.isValid || submitting}>
                    {submitting ? (
                      <>
                        <span className="inline-flex animate-spin">
                          <Loader2 className="h-4 w-4" />
                        </span>
                        {t("common.deleting")}
                      </>
                    ) : (
                      t("profile.security.action.remove")
                    )}
                  </Button>
                </DialogFooter>
              </form>
            </>
          ) : null}
        </DialogContent>
      </Dialog>
    </Card>
  );
}
