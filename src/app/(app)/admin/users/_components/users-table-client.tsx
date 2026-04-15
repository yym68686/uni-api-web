"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { ChevronLeft, ChevronRight, Loader2, Search, Users, X } from "lucide-react";

import { UserRowActions } from "@/components/admin/user-row-actions";
import { ClientDateTime } from "@/components/common/client-datetime";
import { EmptyState } from "@/components/common/empty-state";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AdminUserItem } from "@/lib/types";
import { useI18n } from "@/components/i18n/i18n-provider";
import { cn } from "@/lib/utils";

type PageToken = number | "ellipsis";

function buildPageTokens(currentPage: number, totalPages: number): PageToken[] {
  if (totalPages <= 1) return [1];

  const pages = new Set<number>([1, totalPages, currentPage - 1, currentPage, currentPage + 1]);
  if (currentPage <= 3) {
    pages.add(2);
    pages.add(3);
  }
  if (currentPage >= totalPages - 2) {
    pages.add(totalPages - 1);
    pages.add(totalPages - 2);
  }

  const ordered = Array.from(pages)
    .filter((page) => page >= 1 && page <= totalPages)
    .sort((a, b) => a - b);

  const tokens: PageToken[] = [];
  for (let index = 0; index < ordered.length; index += 1) {
    const page = ordered[index];
    if (page == null) continue;
    const previous = ordered[index - 1];
    if (previous != null && page - previous > 1) tokens.push("ellipsis");
    tokens.push(page);
  }
  return tokens;
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
  currentPage: number;
  pageSize: number;
  totalItems: number;
  initialEmailQuery: string;
  currentUserId: string | null;
  currentUserRole: string | null;
}

export function AdminUsersTableClient({
  initialItems,
  currentPage,
  pageSize,
  totalItems,
  initialEmailQuery,
  currentUserId,
  currentUserRole
}: AdminUsersTableClientProps) {
  const pathname = usePathname();
  const router = useRouter();
  const searchParams = useSearchParams();
  const { locale, t } = useI18n();
  const [items, setItems] = React.useState<AdminUserItem[]>(initialItems);
  const [totalCount, setTotalCount] = React.useState(totalItems);
  const [emailQuery, setEmailQuery] = React.useState(initialEmailQuery);
  const [isPending, startTransition] = React.useTransition();
  const balanceFormatter = React.useMemo(() => {
    return new Intl.NumberFormat(locale, {
      style: "currency",
      currency: "USD",
      currencyDisplay: "narrowSymbol",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    });
  }, [locale]);

  React.useEffect(() => {
    setItems(initialItems);
    setTotalCount(totalItems);
  }, [initialItems, totalItems]);

  const activeEmailQuery = React.useMemo(() => searchParams.get("email")?.trim() ?? "", [searchParams]);

  React.useEffect(() => {
    setEmailQuery(activeEmailQuery);
  }, [activeEmailQuery]);

  const totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
  const page = Math.min(currentPage, totalPages);
  const pageTokens = React.useMemo(() => buildPageTokens(page, totalPages), [page, totalPages]);
  const hasActiveSearch = activeEmailQuery.length > 0;

  function createPageHref(nextPage: number, nextEmail = activeEmailQuery) {
    const params = new URLSearchParams(searchParams.toString());
    const normalizedEmail = nextEmail.trim();
    if (normalizedEmail.length > 0) params.set("email", normalizedEmail);
    else params.delete("email");
    if (nextPage <= 1) params.delete("page");
    else params.set("page", String(nextPage));
    const query = params.toString();
    return query.length > 0 ? `${pathname}?${query}` : pathname;
  }

  function navigate(nextPage: number, nextEmail = activeEmailQuery) {
    startTransition(() => {
      router.replace(createPageHref(nextPage, nextEmail), { scroll: false });
    });
  }

  function updateUser(next: AdminUserItem) {
    setItems((prev) => prev.map((u) => (u.id === next.id ? next : u)));
  }

  function deleteUser(id: string) {
    setItems((prev) => prev.filter((u) => u.id !== id));
    const nextTotal = Math.max(0, totalCount - 1);
    setTotalCount(nextTotal);

    const nextTotalPages = Math.max(1, Math.ceil(nextTotal / pageSize));
    if (currentPage > nextTotalPages) {
      router.replace(createPageHref(nextTotalPages), { scroll: false });
    }
  }

  function handleSearchSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const normalized = emailQuery.trim();
    if (normalized === activeEmailQuery) {
      if (page !== 1) navigate(1, normalized);
      return;
    }
    navigate(1, normalized);
  }

  function clearSearch() {
    if (!hasActiveSearch) {
      setEmailQuery("");
      return;
    }
    setEmailQuery("");
    navigate(1, "");
  }

  return (
    <Card>
      <CardContent className="p-0">
        <div className="border-b border-border px-4 py-4 sm:px-6">
          <form
            onSubmit={handleSearchSubmit}
            className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between"
          >
            <div className="w-full max-w-2xl">
              <label
                htmlFor="admin-users-email-search"
                className="mb-2 block text-[11px] font-medium uppercase tracking-[0.18em] text-muted-foreground"
              >
                {t("admin.users.search.label")}
              </label>
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  id="admin-users-email-search"
                  type="search"
                  inputMode="email"
                  autoComplete="off"
                  value={emailQuery}
                  onChange={(event) => setEmailQuery(event.target.value)}
                  placeholder={t("admin.users.search.placeholder")}
                  className={cn(
                    "h-11 rounded-2xl border-border/70 bg-background/70 pl-10 pr-4 font-mono",
                    "shadow-[0_0_0_1px_oklch(var(--border)/0.5),0_14px_40px_oklch(0%_0_0/0.2)]"
                  )}
                />
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="submit"
                size="default"
                className="h-11 rounded-2xl shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                disabled={isPending}
              >
                {isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
                {t("admin.users.search.submit")}
              </Button>
              {(hasActiveSearch || emailQuery.length > 0) ? (
                <Button
                  type="button"
                  variant="outline"
                  size="default"
                  className="h-11 rounded-2xl bg-transparent"
                  onClick={clearSearch}
                  disabled={isPending}
                >
                  <X className="h-4 w-4" />
                  {t("admin.users.search.clear")}
                </Button>
              ) : null}
            </div>
          </form>

          {hasActiveSearch ? (
            <p className="mt-3 text-xs text-muted-foreground">
              {t("admin.users.search.summary", { email: activeEmailQuery, count: totalCount })}
            </p>
          ) : null}
        </div>

        {items.length === 0 && totalCount <= 0 ? (
          <div className="p-6">
            <EmptyState
              icon={(
                <span className="inline-flex uai-float-sm">
                  <Users className="h-6 w-6 text-muted-foreground" />
                </span>
              )}
              title={hasActiveSearch ? t("admin.users.search.empty") : t("admin.users.empty")}
              description={hasActiveSearch ? t("admin.users.search.emptyDesc", { email: activeEmailQuery }) : undefined}
              action={
                hasActiveSearch ? (
                  <Button type="button" variant="outline" className="rounded-xl bg-transparent" onClick={clearSearch}>
                    <X className="h-4 w-4" />
                    {t("admin.users.search.clear")}
                  </Button>
                ) : undefined
              }
            />
          </div>
        ) : (
          <>
            <Table variant="card">
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
                  <TableRow key={u.id} className="uai-cv-auto">
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
                    <TableCell className="font-mono tabular-nums text-sm">{balanceFormatter.format(u.balance)}</TableCell>
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
                    <TableCell className="text-right">
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

            {totalPages > 1 ? (
              <div className="flex flex-col gap-3 border-t border-border px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-6">
                <div className="text-sm text-muted-foreground">
                  {t("admin.users.pagination.summary", {
                    page,
                    pages: totalPages,
                    count: totalCount
                  })}
                </div>

                <div className="flex flex-wrap items-center justify-end gap-2">
                  {page > 1 ? (
                    <Button asChild type="button" variant="outline" size="sm" className="rounded-xl bg-transparent">
                      <Link href={createPageHref(page - 1)} scroll={false}>
                        <ChevronLeft className="h-4 w-4" />
                        {t("common.previous")}
                      </Link>
                    </Button>
                  ) : (
                    <Button type="button" variant="outline" size="sm" className="rounded-xl bg-transparent" disabled>
                      <ChevronLeft className="h-4 w-4" />
                      {t("common.previous")}
                    </Button>
                  )}

                  {pageTokens.map((token, index) =>
                    token === "ellipsis" ? (
                      <span key={`ellipsis-${index}`} className="px-1 text-sm text-muted-foreground">
                        …
                      </span>
                    ) : token === page ? (
                      <span
                        key={token}
                        aria-current="page"
                        className={cn(
                          buttonVariants({ variant: "outline", size: "sm" }),
                          "min-w-9 rounded-xl border-primary/30 bg-primary/12 text-foreground",
                          "shadow-[0_0_0_1px_oklch(var(--primary)/0.18),0_12px_30px_oklch(var(--primary)/0.12),inset_0_1px_0_0_oklch(var(--foreground)/0.08)]"
                        )}
                      >
                        {token}
                      </span>
                    ) : (
                      <Button
                        key={token}
                        asChild
                        type="button"
                        variant="outline"
                        size="sm"
                        className="min-w-9 rounded-xl bg-transparent"
                      >
                        <Link href={createPageHref(token)} scroll={false}>
                          {token}
                        </Link>
                      </Button>
                    )
                  )}

                  {page < totalPages ? (
                    <Button asChild type="button" variant="outline" size="sm" className="rounded-xl bg-transparent">
                      <Link href={createPageHref(page + 1)} scroll={false}>
                        {t("common.next")}
                        <ChevronRight className="h-4 w-4" />
                      </Link>
                    </Button>
                  ) : (
                    <Button type="button" variant="outline" size="sm" className="rounded-xl bg-transparent" disabled>
                      {t("common.next")}
                      <ChevronRight className="h-4 w-4" />
                    </Button>
                  )}
                </div>
              </div>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  );
}
