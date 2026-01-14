import { AdminUsersTableClient } from "./users-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { AdminUsersListResponse } from "@/lib/types";

function isAdminUsersListResponse(value: unknown): value is AdminUsersListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
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
  currentUserId: string | null;
  currentUserRole: string | null;
}

export async function AdminUsersContent({ currentUserId, currentUserRole }: AdminUsersContentProps) {
  const users = (await getUsers()) ?? [];

  return <AdminUsersTableClient initialItems={users} currentUserId={currentUserId} currentUserRole={currentUserRole} />;
}
