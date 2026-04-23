const CLIENT_IP_HEADER_CANDIDATES = [
  "cf-connecting-ip",
  "true-client-ip",
  "x-forwarded-for",
  "x-real-ip"
] as const;

function normalizeClientIpCandidate(value: string | null | undefined) {
  if (typeof value !== "string") return null;
  const candidate = value.split(",")[0]?.trim();
  if (!candidate) return null;
  return candidate.slice(0, 64);
}

export function resolveForwardedClientIp(headers: Headers) {
  for (const headerName of CLIENT_IP_HEADER_CANDIDATES) {
    const candidate = normalizeClientIpCandidate(headers.get(headerName));
    if (candidate) return candidate;
  }
  return null;
}

export function copyForwardedClientIpHeaders(source: Headers, target: Headers) {
  const cfConnectingIp = normalizeClientIpCandidate(source.get("cf-connecting-ip"));
  if (cfConnectingIp) target.set("cf-connecting-ip", cfConnectingIp);

  const trueClientIp = normalizeClientIpCandidate(source.get("true-client-ip"));
  if (trueClientIp) target.set("true-client-ip", trueClientIp);

  const clientIp = resolveForwardedClientIp(source);
  if (!clientIp) return;

  target.set("x-real-ip", clientIp);
  target.set("x-forwarded-for", clientIp);
}
