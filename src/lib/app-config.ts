import "server-only";

import { DEFAULT_CNY_PER_USD, parseCnyPerUsd } from "@/lib/currency";

export function getAppName() {
  const name = process.env.APP_NAME?.trim();
  return name && name.length > 0 ? name : "MyApp";
}

function normalizeBaseUrl(raw: string) {
  return raw.trim().replace(/\/+$/, "");
}

export function getPublicApiBaseUrl() {
  const raw = process.env.PUBLIC_API_BASE_URL?.trim();
  if (!raw) return null;
  const normalized = normalizeBaseUrl(raw);
  return normalized.length > 0 ? normalized : null;
}

export function getPublicAppBaseUrl() {
  const raw = process.env.APP_PUBLIC_URL?.trim();
  if (!raw) return null;
  const normalized = normalizeBaseUrl(raw);
  return normalized.length > 0 ? normalized : null;
}

export function getDisplayCnyPerUsd() {
  return parseCnyPerUsd(
    process.env.NEXT_PUBLIC_CNY_PER_USD ??
      process.env.PUBLIC_CNY_PER_USD ??
      process.env.ZHUPAY_CNY_PER_CREDIT ??
      DEFAULT_CNY_PER_USD
  );
}
