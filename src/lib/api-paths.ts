export const API_PATHS = {
  announcements: "/api/announcements",
  keys: "/api/keys",
  usage: "/api/usage"
} as const;

export function logsListApiPath(limit: number, offset: number) {
  return `/api/logs?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`;
}

