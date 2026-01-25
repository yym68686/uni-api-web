import "server-only";

import { cache } from "react";

import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { AuthMethodsResponse } from "@/lib/types";

function isAuthMethodsResponse(value: unknown): value is AuthMethodsResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (typeof v.passwordSet !== "boolean") return false;
  if (!Array.isArray(v.oauth)) return false;
  for (const item of v.oauth) {
    if (!item || typeof item !== "object") return false;
    const row = item as Record<string, unknown>;
    if (typeof row.id !== "string") return false;
    if (typeof row.provider !== "string") return false;
    if (typeof row.email !== "string") return false;
    if (typeof row.createdAt !== "string") return false;
  }
  return true;
}

export const getAuthMethods = cache(async (): Promise<AuthMethodsResponse | null> => {
  try {
    const res = await fetch(buildBackendUrl("/auth/methods"), {
      cache: "force-cache",
      next: { tags: [CACHE_TAGS.authMethods], revalidate: 30 },
      headers: await getBackendAuthHeadersCached()
    });
    if (!res.ok) return null;
    const json: unknown = await res.json().catch(() => null);
    if (!isAuthMethodsResponse(json)) return null;
    return json;
  } catch {
    return null;
  }
});
