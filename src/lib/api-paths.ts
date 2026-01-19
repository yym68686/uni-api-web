export const API_PATHS = {
  announcements: "/api/announcements",
  billingLedger: "/api/billing/ledger",
  keys: "/api/keys",
  models: "/api/models",
  usage: "/api/usage"
} as const;

export function logsListApiPath(limit: number, offset: number) {
  return `/api/logs?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`;
}

export function billingLedgerListApiPath(limit: number, offset: number) {
  return `/api/billing/ledger?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`;
}
