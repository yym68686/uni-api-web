import { Shield, Users } from "lucide-react";

import { UserRowActions } from "@/components/admin/user-row-actions";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { getCurrentUser } from "@/lib/current-user";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";
import type { AdminUsersListResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

function isAdminUsersListResponse(value: unknown): value is AdminUsersListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function formatDateTime(locale: string, value: string | null | undefined) {
  if (!value) return "—";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return value;
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(dt);
}

function formatBalance(locale: string, value: number) {
  return new Intl.NumberFormat(locale).format(value);
}

function statusVariant(bannedAt: string | null | undefined) {
  return bannedAt ? "destructive" : "success";
}

function groupBadgeVariant(group: string) {
  if (group === "admin") return "warning";
  if (group === "default") return "default";
  return "secondary";
}

async function getUsers() {
  const res = await fetch(buildBackendUrl("/admin/users"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminUsersListResponse(json)) return null;
  return json.items;
}

export default async function AdminUsersPage() {
  const locale = await getRequestLocale();
  const me = await getCurrentUser();
  const isAdmin = me?.role === "admin" || me?.role === "owner";
  const users = isAdmin ? (await getUsers()) ?? [] : [];

  const current =
    me?.email != null && me.email.length > 0
      ? t(locale, "admin.currentUser", { email: me.email })
      : t(locale, "admin.currentUser", { email: t(locale, "common.unknown") });

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t(locale, "app.admin.users")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t(locale, "admin.users.subtitle", { current })}</p>
        </div>
      </div>

      {!isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-muted-foreground" />
              {t(locale, "admin.forbidden")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.users.forbidden")}</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Users className="h-5 w-5 text-primary" />
              {t(locale, "admin.users.card.title")}
            </CardTitle>
            <CardDescription>{t(locale, "admin.users.card.desc")}</CardDescription>
          </CardHeader>
          <CardContent>
            {users.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
                {t(locale, "admin.users.empty")}
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t(locale, "admin.users.table.email")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.role")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.group")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.status")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.balance")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.keys")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.sessions")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.lastLogin")}</TableHead>
                    <TableHead>{t(locale, "admin.users.table.created")}</TableHead>
                    <TableHead className="w-12 text-right">{t(locale, "keys.table.actions")}</TableHead>
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
                          {u.bannedAt ? t(locale, "admin.users.status.banned") : t(locale, "admin.users.status.active")}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-sm">{formatBalance(locale, u.balance)}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {u.apiKeysActive}/{u.apiKeysTotal}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {u.sessionsActive}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatDateTime(locale, u.lastLoginAt ?? null)}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatDateTime(locale, u.createdAt)}
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
