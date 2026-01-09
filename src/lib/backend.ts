import "server-only";

import { cookies } from "next/headers";
import { cache } from "react";

import { SESSION_COOKIE_NAME } from "@/lib/auth";

function normalizeBaseUrl(raw: string) {
  const trimmed = raw.trim().replace(/\/+$/, "");
  return trimmed;
}

export function getBackendBaseUrl() {
  const raw = process.env.API_BASE_URL ?? "http://localhost:8001/v1";
  return normalizeBaseUrl(raw);
}

export async function getSessionTokenFromCookies() {
  const cookieStore = await cookies();
  return cookieStore.get(SESSION_COOKIE_NAME)?.value ?? null;
}

export async function getBackendAuthHeaders(): Promise<HeadersInit> {
  const token = await getSessionTokenFromCookies();
  const headers: Record<string, string> = {};
  if (token) headers.authorization = `Bearer ${token}`;
  return headers;
}

export const getBackendAuthHeadersCached = cache(getBackendAuthHeaders);

export function buildBackendUrl(path: string) {
  const baseUrl = getBackendBaseUrl();
  const normalizedPath = path.replace(/^\/+/, "");
  return new URL(normalizedPath, `${baseUrl}/`).toString();
}
