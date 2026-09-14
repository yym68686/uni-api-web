export interface Connection {
  base: string;
  key: string;
  session: string;
}
export interface KeyInfo {
  key_id: string;
  prefix: string;
  position: number;
  is_current?: boolean;
}
export interface Distribution {
  sample_count: number;
  mean_ms: number | null;
  last_ms: number | null;
  last_observed_at?: number;
  p50_ms: number | null;
  p95_ms: number | null;
}
export interface Stats {
  started: number;
  success: number;
  failed: number;
  success_rate_denominator: number;
  success_rate: number | null;
  inflight: number;
  skipped: number;
  client_cancelled: number;
  hedge_cancelled: number;
  first_output?: Distribution;
  request_to_dispatch?: Distribution;
  last_success_at: number | null;
}
export interface Channel {
  provider: string;
  model: string;
  upstream_model: string;
  engine: string;
  endpoint: string;
  stream: boolean;
  eligible: boolean;
  reason: string;
  stats: Stats;
  points?: (Stats & { timestamp: number; covered: boolean })[];
}
export interface Catalog {
  data: Channel[];
  snapshot_revision: string;
}
export interface Metrics extends Catalog {
  generated_at: number;
  from: number;
  to: number;
  window_minutes: number;
  coverage: string;
  dropped: number;
  collection_started_at: number;
  order: string;
  statistics_scope: string;
}
export interface BalanceKey {
  status: string;
  kind?: string;
  amount?: number | null;
  currency?: string;
  unlimited?: boolean;
  checked_at?: number;
  position: number;
  windows?: { window: string; remaining: number }[];
}
export interface Balance {
  provider: string;
  status: string;
  key_count?: number;
  omitted_keys?: number;
  keys?: BalanceKey[];
}
