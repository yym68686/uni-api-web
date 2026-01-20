"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Ban, Coins, Loader2, MoreVertical, Shield, Tag, Trash2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AdminUserDeleteResponse, AdminUserItem, AdminUserUpdateResponse } from "@/lib/types";
import type { MessageKey, MessageVars } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
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

function createBalanceSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.object({
    balance: z.coerce.number().int().min(0, t("validation.balanceMin")).max(1_000_000_000, t("validation.balanceMax"))
  });
}

type BalanceFormValues = z.infer<ReturnType<typeof createBalanceSchema>>;

const roleSchema = z.object({
  role: z.enum(["owner", "admin", "billing", "developer", "viewer"])
});

type RoleFormValues = z.infer<typeof roleSchema>;

function createGroupSchema(t: (key: MessageKey, vars?: MessageVars) => string) {
  return z.object({
    group: z
      .string()
      .trim()
      .min(1, t("validation.groupRequired"))
      .max(64, t("validation.maxChars", { max: 64 }))
      .refine((v) => !v.includes("\n") && !v.includes("\r"), t("validation.groupInvalidChars"))
  });
}

type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>;

interface UserRowActionsProps {
  user: AdminUserItem;
  currentUserId: string | null;
  currentUserRole: string | null;
  onUpdated?: (next: AdminUserItem) => void;
  onDeleted?: (id: string) => void;
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

export function UserRowActions({ user, currentUserId, currentUserRole, onUpdated, onDeleted, className }: UserRowActionsProps) {
  const router = useRouter();
  const [balanceOpen, setBalanceOpen] = React.useState(false);
  const [groupOpen, setGroupOpen] = React.useState(false);
  const [roleOpen, setRoleOpen] = React.useState(false);
  const [banOpen, setBanOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const { t } = useI18n();

  const isSelf = currentUserId != null && currentUserId === user.id;
  const isBanned = Boolean(user.bannedAt);
  const canManageOwner = currentUserRole === "owner";
  const isTargetOwner = user.role === "owner";
  const ownerProtected = isTargetOwner && !canManageOwner;

  const balanceSchema = React.useMemo(() => createBalanceSchema(t), [t]);
  const groupSchema = React.useMemo(() => createGroupSchema(t), [t]);

  const form = useForm<BalanceFormValues>({
    defaultValues: { balance: user.balance },
    mode: "onChange"
  });

  const roleForm = useForm<RoleFormValues>({
    defaultValues: {
      role:
        user.role === "owner" ||
        user.role === "admin" ||
        user.role === "billing" ||
        user.role === "developer" ||
        user.role === "viewer"
          ? user.role
          : "developer"
    },
    mode: "onChange"
  });

  const selectedRole = roleForm.watch("role");

  const groupForm = useForm<GroupFormValues>({
    defaultValues: { group: user.group || "default" },
    mode: "onChange"
  });

  React.useEffect(() => {
    if (!balanceOpen) return;
    form.reset({ balance: user.balance });
  }, [balanceOpen, form, user.balance]);

  React.useEffect(() => {
    if (!groupOpen) return;
    groupForm.reset({ group: user.group || "default" });
  }, [groupForm, groupOpen, user.group]);

  React.useEffect(() => {
    if (!roleOpen) return;
    roleForm.reset({
      role:
        user.role === "owner" ||
        user.role === "admin" ||
        user.role === "billing" ||
        user.role === "developer" ||
        user.role === "viewer"
          ? user.role
          : "developer"
    });
  }, [roleForm, roleOpen, user.role]);

  async function patch(payload: { balance?: number; banned?: boolean; group?: string | null; role?: string | null }) {
    const res = await fetch(`/api/admin/users/${encodeURIComponent(user.id)}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload)
    });
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) throw new Error(readMessage(json, t("common.operationFailed")));
    const next = json as AdminUserUpdateResponse;
    return next.item;
  }

  async function updateBalance(values: BalanceFormValues) {
    setSubmitting(true);
    try {
      const parsed = balanceSchema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? t("common.formInvalid"));
        return;
      }
      onUpdated?.({ ...user, balance: parsed.data.balance });
      const next = await patch({ balance: parsed.data.balance });
      onUpdated?.(next);
      toast.success(t("admin.users.toast.balanceUpdated"));
      setBalanceOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(user);
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function updateGroup(values: GroupFormValues) {
    setSubmitting(true);
    try {
      const parsed = groupSchema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? t("common.formInvalid"));
        return;
      }
      onUpdated?.({ ...user, group: parsed.data.group });
      const next = await patch({ group: parsed.data.group });
      onUpdated?.(next);
      toast.success(t("admin.users.toast.groupUpdated"));
      setGroupOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(user);
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function resetGroup() {
    setSubmitting(true);
    try {
      onUpdated?.({ ...user, group: "default" });
      const next = await patch({ group: null });
      onUpdated?.(next);
      toast.success(t("admin.users.toast.groupReset"));
      setGroupOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(user);
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function updateRole(values: RoleFormValues) {
    setSubmitting(true);
    try {
      const parsed = roleSchema.safeParse(values);
      if (!parsed.success) {
        toast.error(t("common.formInvalid"));
        return;
      }
      onUpdated?.({ ...user, role: parsed.data.role });
      const next = await patch({ role: parsed.data.role });
      onUpdated?.(next);
      toast.success(t("admin.users.toast.roleUpdated"));
      setRoleOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(user);
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleBan() {
    setSubmitting(true);
    try {
      const nextBanned = !isBanned;
      onUpdated?.({ ...user, bannedAt: nextBanned ? new Date().toISOString() : null });
      const next = await patch({ banned: nextBanned });
      onUpdated?.(next);
      toast.success(isBanned ? t("admin.users.toast.unbanned") : t("admin.users.toast.banned"));
      setBanOpen(false);
      if (!onUpdated) router.refresh();
    } catch (err) {
      onUpdated?.(user);
      toast.error(err instanceof Error ? err.message : t("common.operationFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  async function removeUser() {
    setSubmitting(true);
    try {
      const res = await fetch(`/api/admin/users/${encodeURIComponent(user.id)}`, { method: "DELETE" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("common.deleteFailed")));
      void (json as AdminUserDeleteResponse);
      toast.success(t("admin.users.toast.deleted"));
      setDeleteOpen(false);
      onDeleted?.(user.id);
      if (!onDeleted) router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("common.deleteFailed"));
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
            className={cn("h-8 w-8 rounded-xl", className)}
            aria-label={t("common.actions")}
          >
            <MoreVertical className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            disabled={ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setGroupOpen(true);
            }}
          >
            <Tag className="mr-2 h-4 w-4" />
            {t("admin.users.actions.setGroup")}
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={isSelf || ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setRoleOpen(true);
            }}
          >
            <Shield className="mr-2 h-4 w-4" />
            {t("admin.users.actions.setRole")}
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setBalanceOpen(true);
            }}
          >
            <Coins className="mr-2 h-4 w-4" />
            {t("admin.users.actions.adjustBalance")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            disabled={isSelf || ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setBanOpen(true);
            }}
          >
            <Ban className="mr-2 h-4 w-4" />
            {isBanned ? t("admin.users.actions.unban") : t("admin.users.actions.ban")}
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={isSelf || ownerProtected}
            className="text-destructive focus:text-destructive"
            onSelect={(e) => {
              e.preventDefault();
              setDeleteOpen(true);
            }}
          >
            <Trash2 className="mr-2 h-4 w-4" />
            {t("common.delete")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={roleOpen} onOpenChange={setRoleOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.users.role.title")}</DialogTitle>
            <DialogDescription>{t("admin.users.role.desc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void roleForm.handleSubmit(updateRole)(e);
            }}
          >
            <div className="space-y-2">
              <Label>{t("admin.users.table.role")}</Label>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                {(
                  [
                    { value: "owner", label: "Owner" },
                    { value: "admin", label: "Admin" },
                    { value: "billing", label: "Billing" },
                    { value: "developer", label: "Dev" },
                    { value: "viewer", label: "Viewer" }
                  ] as const
                ).map((opt) => {
                  const active = selectedRole === opt.value;
                  const disabled =
                    (opt.value === "owner" && !canManageOwner) || (isTargetOwner && !canManageOwner);
                  return (
                    <Button
                      key={opt.value}
                      type="button"
                      variant={active ? "default" : "outline"}
                      className="rounded-xl"
                      disabled={disabled}
                      onClick={() => roleForm.setValue("role", opt.value, { shouldValidate: true })}
                    >
                      {opt.label}
                    </Button>
                  );
                })}
              </div>
              {!canManageOwner ? (
                <p className="text-xs text-muted-foreground">{t("admin.users.role.ownerOnlyHelp")}</p>
              ) : null}
            </div>

            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setRoleOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!roleForm.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <span className="inline-flex animate-spin">
                      <Loader2 className="h-4 w-4" />
                    </span>
                    {t("common.saving")}
                  </>
                ) : (
                  t("common.save")
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={groupOpen} onOpenChange={setGroupOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.users.group.title")}</DialogTitle>
            <DialogDescription>{t("admin.users.group.desc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void groupForm.handleSubmit(updateGroup)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`group-${user.id}`}>{t("admin.users.table.group")}</Label>
              <Input
                id={`group-${user.id}`}
                placeholder="default"
                className="font-mono"
                {...groupForm.register("group")}
              />
              {groupForm.formState.errors.group ? (
                <p className="text-xs text-destructive">{groupForm.formState.errors.group.message}</p>
              ) : (
                <p className="text-xs text-muted-foreground">{t("admin.users.group.hint")}</p>
              )}
            </div>

            <DialogFooter className="justify-between">
              <Button type="button" variant="ghost" disabled={submitting} onClick={() => void resetGroup()}>
                {t("admin.users.group.reset")}
              </Button>
              <div className="flex gap-2">
                <Button type="button" variant="ghost" onClick={() => setGroupOpen(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={!groupForm.formState.isValid || submitting}>
                  {submitting ? (
                    <>
                      <span className="inline-flex animate-spin">
                        <Loader2 className="h-4 w-4" />
                      </span>
                      {t("common.saving")}
                    </>
                  ) : (
                    t("common.save")
                  )}
                </Button>
              </div>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={balanceOpen} onOpenChange={setBalanceOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.users.balance.title")}</DialogTitle>
            <DialogDescription>{t("admin.users.balance.desc")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(updateBalance)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`balance-${user.id}`}>{t("admin.users.table.balance")}</Label>
              <Input
                id={`balance-${user.id}`}
                type="number"
                inputMode="numeric"
                className="font-mono"
                {...form.register("balance")}
              />
              {form.formState.errors.balance ? (
                <p className="text-xs text-destructive">{form.formState.errors.balance.message}</p>
              ) : null}
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setBalanceOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!form.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <span className="inline-flex animate-spin">
                      <Loader2 className="h-4 w-4" />
                    </span>
                    {t("common.saving")}
                  </>
                ) : (
                  t("common.save")
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={banOpen} onOpenChange={setBanOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t("admin.users.ban.title", {
                action: isBanned ? t("admin.users.actions.unban") : t("admin.users.actions.ban")
              })}
            </DialogTitle>
            <DialogDescription>
              {isBanned
                ? t("admin.users.ban.desc.banned")
                : t("admin.users.ban.desc.active")}
            </DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{user.email}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{user.id}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setBanOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="button" variant={isBanned ? "outline" : "destructive"} disabled={submitting} onClick={() => void toggleBan()}>
              {submitting ? (
                <>
                  <span className="inline-flex animate-spin">
                    <Loader2 className="h-4 w-4" />
                  </span>
                  {t("common.working")}
                </>
              ) : isBanned ? (
                t("admin.users.actions.unban")
              ) : (
                t("admin.users.actions.ban")
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("admin.users.delete.title")}</DialogTitle>
            <DialogDescription>{t("admin.users.delete.desc")}</DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{user.email}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{user.id}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void removeUser()}>
              {submitting ? (
                <>
                  <span className="inline-flex animate-spin">
                    <Loader2 className="h-4 w-4" />
                  </span>
                  {t("common.deleting")}
                </>
              ) : (
                t("common.delete")
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
