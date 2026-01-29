export const DEVICE_ID_COOKIE_NAME = "uai_device_id";

function readCookie(name: string) {
  if (typeof document === "undefined") return null;
  const prefix = `${encodeURIComponent(name)}=`;
  const parts = document.cookie.split(";");
  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed.startsWith(prefix)) continue;
    const raw = trimmed.slice(prefix.length);
    try {
      return decodeURIComponent(raw);
    } catch {
      return raw;
    }
  }
  return null;
}

function writeCookie(name: string, value: string) {
  const encoded = `${encodeURIComponent(name)}=${encodeURIComponent(value)}`;
  const maxAgeSeconds = 60 * 60 * 24 * 365 * 2;
  document.cookie = `${encoded}; Path=/; Max-Age=${maxAgeSeconds}; SameSite=Lax`;
}

function createId() {
  try {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID();
    }
  } catch {
    // ignore
  }
  const rand = Math.random().toString(36).slice(2);
  return `uai_${Date.now().toString(36)}_${rand}`;
}

export function ensureDeviceIdCookie() {
  if (typeof document === "undefined") return null;
  const existing = readCookie(DEVICE_ID_COOKIE_NAME);
  if (existing && existing.length >= 8) return existing;
  const created = createId();
  writeCookie(DEVICE_ID_COOKIE_NAME, created);
  return created;
}

