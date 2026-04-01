import { AdminUsersTableClient } from "./users-table-client";
import { redirect } from "next/navigation";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { AdminUsersListResponse } from "@/lib/types";

function isAdminUsersListResponse(value: unknown): value is AdminUsersListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value) || !("total" in value)) return false;
  const items = (value as { items?: unknown }).items;
  const total = (value as { total?: unknown }).total;
  return Array.isArray(items) && typeof total === "number";
}

function getParam(searchParams: Record<string, string | string[] | undefined>, key: string) {
  const value = searchParams[key];
  return Array.isArray(value) ? value[0] : value;
}

function parsePage(value: string | undefined) {
  if (!value) return 1;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 1) return 1;
  return parsed;
}

function buildPageHref(searchParams: Record<string, string | string[] | undefined>, page: number) {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(searchParams)) {
    if (key === "page" || value == null) continue;
    if (Array.isArray(value)) {
      for (const entry of value) qs.append(key, entry);
      continue;
    }
    qs.set(key, value);
  }
  if (page > 1) qs.set("page", String(page));
  const query = qs.toString();
  return query.length > 0 ? `/admin/users?${query}` : "/admin/users";
}

async function getUsers(page: number) {
  const offset = (page - 1) * ADMIN_USERS_PAGE_SIZE;
  const res = await fetch(buildBackendUrl(`/admin/users?limit=${ADMIN_USERS_PAGE_SIZE}&offset=${offset}`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminUsersListResponse(json)) return null;
  return json;
}

export const ADMIN_USERS_PAGE_SIZE = 50;

interface AdminUsersContentProps {
  currentUserId: string | null;
  currentUserRole: string | null;
  searchParams: Record<string, string | string[] | undefined>;
}

export async function AdminUsersContent({ currentUserId, currentUserRole, searchParams }: AdminUsersContentProps) {
  const requestedPage = parsePage(getParam(searchParams, "page"));
  const response = await getUsers(requestedPage);
  const totalItems = response?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(totalItems / ADMIN_USERS_PAGE_SIZE));
  const currentPage = Math.min(requestedPage, totalPages);

  if (response && currentPage !== requestedPage) {
    redirect(buildPageHref(searchParams, currentPage));
  }

  const users = response?.items ?? [];

  return (
    <AdminUsersTableClient
      initialItems={users}
      currentPage={currentPage}
      pageSize={ADMIN_USERS_PAGE_SIZE}
      totalItems={totalItems}
      currentUserId={currentUserId}
      currentUserRole={currentUserRole}
    />
  );
}
