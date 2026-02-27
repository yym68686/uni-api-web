import "server-only";

import { cache } from "react";
import { cookies } from "next/headers";

import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";

export interface CurrentUser {
  id: string;
  email: string;
  role: string;
  group: string;
  balance: number;
  orgId: string;
  createdAt: string;
  lastLoginAt: string | null;
}

function isCurrentUser(value: unknown): value is CurrentUser {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.id === "string" &&
    typeof v.email === "string" &&
    typeof v.role === "string" &&
    typeof v.group === "string" &&
    typeof v.balance === "number" &&
    typeof v.orgId === "string" &&
    typeof v.createdAt === "string" &&
    (v.lastLoginAt === null || typeof v.lastLoginAt === "string")
  );
}

export const getCurrentUser = cache(async (): Promise<CurrentUser | null> => {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  if (!isLoggedInCookie(token)) return null;
  try {
    const res = await fetch(buildBackendUrl("/auth/me"), {
      cache: "no-store",
      headers: await getBackendAuthHeadersCached()
    });
    if (!res.ok) return null;
    const json: unknown = await res.json();
    if (!isCurrentUser(json)) return null;
    return json;
  } catch {
    return null;
  }
});
