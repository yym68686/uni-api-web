import { Users } from "lucide-react";

import { UserRowActions } from "@/components/admin/user-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import { t } from "@/lib/i18n/messages";
import type { AdminUsersListResponse } from "@/lib/types";

function isAdminUsersListResponse(value: unknown): value is AdminUsersListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
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
    cache: "force-cache",
    next: { tags: ["admin:users"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminUsersListResponse(json)) return null;
  return json.items;
}

interface AdminUsersContentProps {
  locale: Locale;
  currentUserId: string | null;
  currentUserRole: string | null;
}

export async function AdminUsersContent({ locale, currentUserId, currentUserRole }: AdminUsersContentProps) {
  const users = (await getUsers()) ?? [];

  return (
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
                  <TableCell className="font-mono tabular-nums text-sm">{formatBalance(locale, u.balance)}</TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {u.apiKeysActive}/{u.apiKeysTotal}
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    {u.sessionsActive}
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    <ClientDateTime value={u.lastLoginAt ?? null} locale={locale} />
                  </TableCell>
                  <TableCell className="font-mono tabular-nums text-xs text-muted-foreground">
                    <ClientDateTime value={u.createdAt} locale={locale} />
                  </TableCell>
                  <TableCell className="p-2 text-right">
                    <UserRowActions user={u} currentUserId={currentUserId} currentUserRole={currentUserRole} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
