import { Shield, Users } from "lucide-react";

import { UserRowActions } from "@/components/admin/user-row-actions";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";
import type { AdminUsersListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isAdminUsersListResponse(value: unknown): value is AdminUsersListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatDateTime(value: string | null | undefined) {
  if (!value) return "—";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return value;
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(dt);
}

function formatBalance(value: number) {
  return new Intl.NumberFormat("en").format(value);
}

function statusVariant(bannedAt: string | null | undefined) {
  return bannedAt ? "destructive" : "success";
}

function groupBadgeVariant(group: string) {
  if (group === "admin") return "warning";
  if (group === "default") return "default";
  return "secondary";
}

async function getMe() {
  const res = await fetch(buildBackendUrl("/auth/me"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!json || typeof json !== "object") return null;
  const id = (json as { id?: unknown }).id;
  const role = (json as { role?: unknown }).role;
  const email = (json as { email?: unknown }).email;
  if (typeof id !== "string" || typeof role !== "string" || typeof email !== "string") return null;
  return { id, role, email };
}

async function getUsers() {
  const res = await fetch(buildBackendUrl("/admin/users"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminUsersListResponse(json)) return null;
  return json.items;
}

export default async function AdminUsersPage() {
  const me = await getMe();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const users = isAdmin ? (await getUsers()) ?? [] : [];

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
          <p className="mt-1 text-sm text-muted-foreground">管理员查看/管理用户（当前：{me?.email ?? "unknown"}）。</p>
        </div>
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-muted-foreground" />
              Forbidden
            </CardTitle>
            <CardDescription>你不是管理员，无法管理用户。</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Users className="h-5 w-5 text-primary" />
              All users
            </CardTitle>
            <CardDescription>支持封禁、删除与余额调整。</CardDescription>
          </CardHeader>
          <CardContent>
            {users.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
                暂无用户
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Email</TableHead>
                    <TableHead>Role</TableHead>
                    <TableHead>Group</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Balance</TableHead>
                    <TableHead>Keys</TableHead>
                    <TableHead>Sessions</TableHead>
                    <TableHead>Last login</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead className="w-12 text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((u) => (
                    <TableRow key={u.id}>
                      <TableCell className="font-medium text-foreground">{u.email}</TableCell>
                      <TableCell>
                        <Badge
                          variant={u.role === "admin" || u.role === "owner" ? "warning" : "default"}
                          className="capitalize"
                        >
                          {u.role}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={groupBadgeVariant(u.group)} className="font-mono">
                          {u.group}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusVariant(u.bannedAt ?? null)}>
                          {u.bannedAt ? "Banned" : "Active"}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-sm">{formatBalance(u.balance)}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {u.apiKeysActive}/{u.apiKeysTotal}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {u.sessionsActive}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatDateTime(u.lastLoginAt ?? null)}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatDateTime(u.createdAt)}
                      </TableCell>
                      <TableCell className="p-2 text-right">
                        <UserRowActions
                          user={u}
                          currentUserId={me?.id ?? null}
                          currentUserRole={me?.role ?? null}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
