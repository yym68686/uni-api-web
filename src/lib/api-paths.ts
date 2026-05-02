export const API_PATHS = {
  announcements: "/api/announcements",
  authMe: "/api/auth/me",
  billingLedger: "/api/billing/ledger",
  billingTopupCheckout: "/api/billing/topup/checkout",
  billingTopupStatus: "/api/billing/topup/status",
  inviteVisit: "/api/invite/visit",
  inviteSummary: "/api/invite/summary",
  keys: "/api/keys",
  models: "/api/models",
  usage: "/api/usage"
} as const;

export function usageApiPath(timeZone?: string | null, days?: number | null) {
  const tz = typeof timeZone === "string" ? timeZone.trim() : "";
  const params = new URLSearchParams();
  if (tz.length > 0) params.set("tz", tz);
  if (typeof days === "number" && Number.isFinite(days)) {
    params.set("days", String(Math.trunc(days)));
  }
  const qs = params.toString();
  if (qs.length <= 0) return API_PATHS.usage;
  return `${API_PATHS.usage}?${qs}`;
}

export function logsListApiPath(limit: number, offset: number) {
  return `/api/logs?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`;
}

export function logsExportApiPath() {
  return "/api/logs/export";
}

export function billingLedgerListApiPath(limit: number, offset: number) {
  return `/api/billing/ledger?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`;
}

export function billingTopupStatusApiPath(requestId: string) {
  return `${API_PATHS.billingTopupStatus}?request_id=${encodeURIComponent(requestId)}`;
}
