"use client";

export function getPublicApiBaseUrlClient(): string | null {
  if (typeof document === "undefined") return null;
  const value = document.documentElement.dataset.publicApiBaseUrl;
  if (!value) return null;
  const normalized = value.trim().replace(/\/+$/, "");
  return normalized.length > 0 ? normalized : null;
}
