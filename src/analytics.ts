import type { Catalog, Channel, Metrics, Stats } from "./types";
export const ranges = [["5m","5 分钟"],["15m","15 分钟"],["1h","1 小时"],["24h","24 小时"],["7d","近 7 天"],["30d","近 30 天"],["today","今天"],["week","本周"],["month","本月"],["year","今年"],["all","全部"]] as const;
export function emptyStats(): Stats { return {started:0,success:0,failed:0,success_rate_denominator:0,success_rate:null,inflight:null,skipped:0,client_cancelled:0,hedge_cancelled:0,last_success_at:null}; }
const identity = (row: Pick<Channel,"provider"|"model"|"upstream_model"|"source_id">) => JSON.stringify([row.source_id||"",row.provider,row.model,row.upstream_model]);
// Analytics merges endpoint and stream dimensions before computing quantiles.
// Configuration owns the membership/order; zero-sample configured routes remain.
export function catalogMetrics(catalog: Catalog | undefined, metrics: Metrics | undefined): Channel[] {
 if (!metrics || !catalog) return [];
 const stats = new Map(metrics.data.map(row => [identity(row), row.stats]));
 return catalog.data.map(row => ({...row,stats:stats.get(identity(row)) || emptyStats()}));
}
export const usd = (value?: number | null) => value == null ? "—" : `$${value.toLocaleString("en-US",{maximumFractionDigits:4,minimumFractionDigits:2})}`;
