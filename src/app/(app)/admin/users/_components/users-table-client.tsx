"use client";

import * as React from "react";
import { Users } from "lucide-react";

import { UserRowActions } from "@/components/admin/user-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AdminUserItem } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";

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

interface AdminUsersTableClientProps {
  initialItems: AdminUserItem[];
  currentUserId: string | null;
  currentUserRole: string | null;
}

export function AdminUsersTableClient({ initialItems, currentUserId, currentUserRole }: AdminUsersTableClientProps) {
  const { locale, t } = useI18n();
  const [items, setItems] = React.useState<AdminUserItem[]>(initialItems);

  function updateUser(next: AdminUserItem) {
    setItems((prev) => prev.map((u) => (u.id === next.id ? next : u)));
  }

  function deleteUser(id: string) {
    setItems((prev) => prev.filter((u) => u.id !== id));
  }

  return (
    <Card>
      {items.length === 0 ? (
        <CardContent className="p-6">
          <EmptyState icon={<Users className="h-6 w-6 text-muted-foreground uai-float-sm" />} title={t("admin.users.empty")} />
        </CardContent>
      ) : (
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("admin.users.table.email")}</TableHead>
                <TableHead>{t("admin.users.table.role")}</TableHead>
                <TableHead>{t("admin.users.table.group")}</TableHead>
                <TableHead>{t("admin.users.table.status")}</TableHead>
                <TableHead>{t("admin.users.table.balance")}</TableHead>
                <TableHead>{t("admin.users.table.keys")}</TableHead>
                <TableHead>{t("admin.users.table.sessions")}</TableHead>
                <TableHead>{t("admin.users.table.lastLogin")}</TableHead>
                <TableHead>{t("admin.users.table.created")}</TableHead>
                <TableHead className="w-12 text-right">{t("keys.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((u) => (
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
                      {u.bannedAt ? t("admin.users.status.banned") : t("admin.users.status.active")}
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
                    <UserRowActions
                      user={u}
                      currentUserId={currentUserId}
                      currentUserRole={currentUserRole}
                      onUpdated={updateUser}
                      onDeleted={deleteUser}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
    </Card>
  );
}
