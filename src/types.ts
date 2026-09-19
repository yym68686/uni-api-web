export interface Connection {
  base: string;
  key: string;
  session: string;
  sourceId?: string;
  account?: boolean;
}
export interface KeyInfo {
  source_id?: string;
  source_name?: string;
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
  input_tokens?: number;
  output_tokens?: number;
  usage_samples?: number;
  cache_rate?: number | null;
  cache_read_tokens?: number;
  cache_samples?: number;
  estimated_cost_usd?: number | null;
  started: number;
  success: number;
  failed: number;
  success_rate_denominator: number;
  success_rate: number | null;
  inflight: number | null;
  skipped: number;
  client_cancelled: number;
  hedge_cancelled: number;
  first_output?: Distribution;
  response_created?: Distribution;
  first_text?: Distribution;
  request_to_dispatch?: Distribution;
  last_success_at: number | null;
}
export interface Channel {
  provider_name?: string;
  history_configured?: boolean;
  source_id?: string;
  source_name?: string;
  provider: string;
  model: string;
  upstream_model: string;
  engine: string;
  endpoint: string;
  stream: boolean | null;
  eligible: boolean;
  reason: string;
  stats: Stats;
  points?: (Stats & { timestamp: number; bucket_start?: number; bucket_end?: number; covered: boolean })[];
}
export interface Catalog {
  unavailable_sources?: string[];
  data: Channel[];
  snapshot_revision: string;
}
export interface Metrics extends Catalog {
  bucket_seconds?: number;
  source_freshness?: { source_id: string; latest_fact_at: number }[];
  filters?: { endpoint: string; stream: string };
  available_endpoints?: string[];
  generated_at: number;
  from: number;
  to: number;
  window_minutes: number;
  coverage: string;
  dropped: number;
  collection_started_at: number;
  order: string;
  statistics_scope: string;
  import?: {
    caught_up: boolean;
    scanning?: boolean;
    remaining_objects: number;
    error_class?: string;
    last_scan_ms?: number;
  };
  total?: Record<string, any>;
  models?: Record<string, any>[];
}
export interface ModelPrice {
  model: string;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  cache_write_1h: number;
  charge_cache_write?: boolean;
  source?: string;
  verified?: boolean;
}
export interface BalanceKey {
  label?: string;
  status: string;
  kind?: string;
  amount?: number | null;
  currency?: string;
  unlimited?: boolean;
  checked_at?: number;
  position: number;
  windows?: { window: string; remaining: number }[];
  actual_cost_usd?: number | null;
  actual_cost_samples?: number;
  actual_cost_source?: string;
}
export interface Balance {
  provider: string;
  status: string;
  key_count?: number;
  omitted_keys?: number;
  keys?: BalanceKey[];
  actual_cost_usd?: number | null;
  actual_cost_samples?: number;
  actual_cost_source?: string;
}
