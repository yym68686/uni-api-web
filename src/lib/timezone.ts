export const TIMEZONE_COOKIE_NAME = "uai_timezone";
export const DEFAULT_TIME_ZONE = "UTC";

export function normalizeTimeZone(value: string | null | undefined): string | null {
  const candidate = typeof value === "string" ? value.trim() : "";
  if (candidate.length <= 0 || candidate.length > 128) return null;

  try {
    Intl.DateTimeFormat("en-US", { timeZone: candidate }).format(new Date(0));
    return candidate;
  } catch {
    return null;
  }
}

export function getBrowserTimeZone(): string {
  if (typeof Intl === "undefined" || typeof Intl.DateTimeFormat !== "function") {
    return DEFAULT_TIME_ZONE;
  }

  return normalizeTimeZone(Intl.DateTimeFormat().resolvedOptions().timeZone) ?? DEFAULT_TIME_ZONE;
}
