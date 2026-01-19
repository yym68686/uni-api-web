import "server-only";

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
