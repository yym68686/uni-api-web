"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { z } from "zod";
import { Ban, Coins, Loader2, MoreVertical, Shield, Tag, Trash2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

import type { AdminUserDeleteResponse, AdminUserItem, AdminUserUpdateResponse } from "@/lib/types";
import { cn } from "@/lib/utils";
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

const balanceSchema = z.object({
  balance: z.coerce.number().int().min(0, "余额不能小于 0").max(1_000_000_000, "余额过大")
});

type BalanceFormValues = z.infer<typeof balanceSchema>;

const roleSchema = z.object({
  role: z.enum(["owner", "admin", "billing", "developer", "viewer"])
});

type RoleFormValues = z.infer<typeof roleSchema>;

const groupSchema = z.object({
  group: z
    .string()
    .trim()
    .min(1, "分组不能为空")
    .max(64, "分组最多 64 个字符")
    .refine((v) => !v.includes("\n") && !v.includes("\r"), "分组包含非法字符")
});

type GroupFormValues = z.infer<typeof groupSchema>;

interface UserRowActionsProps {
  user: AdminUserItem;
  currentUserId: string | null;
  currentUserRole: string | null;
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

export function UserRowActions({ user, currentUserId, currentUserRole, className }: UserRowActionsProps) {
  const router = useRouter();
  const [balanceOpen, setBalanceOpen] = React.useState(false);
  const [groupOpen, setGroupOpen] = React.useState(false);
  const [roleOpen, setRoleOpen] = React.useState(false);
  const [banOpen, setBanOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);

  const isSelf = currentUserId != null && currentUserId === user.id;
  const isBanned = Boolean(user.bannedAt);
  const canManageOwner = currentUserRole === "owner";
  const isTargetOwner = user.role === "owner";
  const ownerProtected = isTargetOwner && !canManageOwner;

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
    if (!res.ok) throw new Error(readMessage(json, "操作失败"));
    void (json as AdminUserUpdateResponse);
  }

  async function updateBalance(values: BalanceFormValues) {
    setSubmitting(true);
    try {
      const parsed = balanceSchema.safeParse(values);
      if (!parsed.success) {
        const issue = parsed.error.issues[0];
        toast.error(issue?.message ?? "表单校验失败");
        return;
      }
      await patch({ balance: parsed.data.balance });
      toast.success("余额已更新");
      setBalanceOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败");
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
        toast.error(issue?.message ?? "表单校验失败");
        return;
      }
      await patch({ group: parsed.data.group });
      toast.success("分组已更新");
      setGroupOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function resetGroup() {
    setSubmitting(true);
    try {
      await patch({ group: null });
      toast.success("分组已重置为 default");
      setGroupOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function updateRole(values: RoleFormValues) {
    setSubmitting(true);
    try {
      const parsed = roleSchema.safeParse(values);
      if (!parsed.success) {
        toast.error("表单校验失败");
        return;
      }
      await patch({ role: parsed.data.role });
      toast.success("角色已更新");
      setRoleOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleBan() {
    setSubmitting(true);
    try {
      await patch({ banned: !isBanned });
      toast.success(isBanned ? "已解除封禁" : "已封禁用户");
      setBanOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function removeUser() {
    setSubmitting(true);
    try {
      const res = await fetch(`/api/admin/users/${encodeURIComponent(user.id)}`, { method: "DELETE" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, "删除失败"));
      void (json as AdminUserDeleteResponse);
      toast.success("用户已删除");
      setDeleteOpen(false);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
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
            className={cn("h-9 w-9 rounded-lg", className)}
            aria-label="User actions"
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
            Set group
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={isSelf || ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setRoleOpen(true);
            }}
          >
            <Shield className="mr-2 h-4 w-4" />
            Set role
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={ownerProtected}
            onSelect={(e) => {
              e.preventDefault();
              setBalanceOpen(true);
            }}
          >
            <Coins className="mr-2 h-4 w-4" />
            Adjust balance
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
            {isBanned ? "Unban" : "Ban"}
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
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={roleOpen} onOpenChange={setRoleOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Set role</DialogTitle>
            <DialogDescription>角色用于权限控制（Owner/Admin/Billing/Developer/Viewer）。</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void roleForm.handleSubmit(updateRole)(e);
            }}
          >
            <div className="space-y-2">
              <Label>Role</Label>
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
                <p className="text-xs text-muted-foreground">只有 Owner 可以授予/调整 Owner 角色。</p>
              ) : null}
            </div>

            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setRoleOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!roleForm.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    Saving…
                  </>
                ) : (
                  "Save"
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={groupOpen} onOpenChange={setGroupOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Set group</DialogTitle>
            <DialogDescription>自定义分组名（例如：admin / team-a / beta）。</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void groupForm.handleSubmit(updateGroup)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`group-${user.id}`}>Group</Label>
              <Input
                id={`group-${user.id}`}
                placeholder="default"
                className="font-mono"
                {...groupForm.register("group")}
              />
              {groupForm.formState.errors.group ? (
                <p className="text-xs text-destructive">{groupForm.formState.errors.group.message}</p>
              ) : (
                <p className="text-xs text-muted-foreground">提示：分组不等于 Role，不会自动赋予管理员权限。</p>
              )}
            </div>

            <DialogFooter className="justify-between">
              <Button type="button" variant="ghost" disabled={submitting} onClick={() => void resetGroup()}>
                Reset to default
              </Button>
              <div className="flex gap-2">
                <Button type="button" variant="ghost" onClick={() => setGroupOpen(false)}>
                  Cancel
                </Button>
                <Button type="submit" disabled={!groupForm.formState.isValid || submitting}>
                  {submitting ? (
                    <>
                      <Loader2 className="h-4 w-4 animate-spin" />
                      Saving…
                    </>
                  ) : (
                    "Save"
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
            <DialogTitle>Adjust balance</DialogTitle>
            <DialogDescription>修改用户余额（仅影响后台存储的余额字段）。</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(e) => {
              void form.handleSubmit(updateBalance)(e);
            }}
          >
            <div className="space-y-2">
              <Label htmlFor={`balance-${user.id}`}>Balance</Label>
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
                Cancel
              </Button>
              <Button type="submit" disabled={!form.formState.isValid || submitting}>
                {submitting ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    Saving…
                  </>
                ) : (
                  "Save"
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={banOpen} onOpenChange={setBanOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{isBanned ? "Unban user?" : "Ban user?"}</DialogTitle>
            <DialogDescription>
              {isBanned
                ? "解除封禁后，用户可重新登录。"
                : "封禁后，该用户将被强制退出并无法登录。"}
            </DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{user.email}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{user.id}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setBanOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant={isBanned ? "outline" : "destructive"} disabled={submitting} onClick={() => void toggleBan()}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Working…
                </>
              ) : isBanned ? (
                "Unban"
              ) : (
                "Ban"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete user?</DialogTitle>
            <DialogDescription>将永久删除用户及其 Sessions / OAuth identities / API keys。</DialogDescription>
          </DialogHeader>

          <div className="rounded-xl border border-border bg-muted/20 p-4">
            <div className="text-sm font-medium text-foreground">{user.email}</div>
            <div className="mt-1 text-xs font-mono text-muted-foreground">{user.id}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={submitting} onClick={() => void removeUser()}>
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Deleting…
                </>
              ) : (
                "Delete"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
