import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { analyticsRequest, initializationRetryInterval } from "./api";
import { isServiceTier, type ServiceTier } from "./serviceTierStatus";
import type { Channel, Connection } from "./types";

export type TierTPS = { samples: number; p50: number; p95: number };
type TierRow = TierTPS & Pick<Channel, "source_id" | "provider" | "model" | "upstream_model"> & { tier: ServiceTier };
type Scope = { range: string; model: string; endpoint: string; stream: string; keyId: string; to?: number };
const tierKey = (row: Pick<TierRow, "source_id" | "provider" | "model" | "upstream_model">, tier: ServiceTier) =>
  JSON.stringify([row.source_id || "", row.provider, row.model, row.upstream_model || row.model, tier]);

export function useChannelTierTPS(connection: Connection, scope: Scope, enabled: boolean) {
  const { keyId, to, ...filters } = scope;
  const params = new URLSearchParams(filters);
  if (to) params.set("to", String(to));
  const separator = keyId.indexOf("::");
  const keySource = separator < 0 ? "" : keyId.slice(0, separator);
  const rawKey = separator < 0 ? keyId : keyId.slice(separator + 2);
  const selectedSource = connection.sourceId === "all" ? "" : connection.sourceId || "";
  if (keySource || selectedSource) params.set("source_id", keySource || selectedSource);
  if (rawKey) params.set("key_id", rawKey);
  const active = enabled && !!to && !(keySource && selectedSource && keySource !== selectedSource);
  const query = useQuery({
    queryKey: ["channel-tier-tps", connection.session, params.toString()],
    queryFn: async ({ signal }) => {
      const result = await analyticsRequest<{data: TierRow[]}>(connection, "/analytics/v1/channel-tier-tps?" + params, signal);
      if (!Array.isArray(result?.data)) throw new Error("档位 TPS 数据无效");
      return result.data;
    },
    enabled: active,
    retry: false,
    staleTime: 30_000,
    refetchInterval: initializationRetryInterval,
  });
  const index = useMemo(() => new Map((query.data || [])
    .filter(row => isServiceTier(row.tier) && row.samples > 0 && Number.isFinite(row.p50) && Number.isFinite(row.p95))
    .map(row => [tierKey(connection.account ? row : {...row,source_id:undefined}, row.tier), row])), [query.data, connection.account]);
  return { get: (row: Channel, tier: ServiceTier) => index.get(tierKey(row, tier)),
    loading: active && query.isPending, failed: active && query.isError };
}

export function ChannelTierTPS({ value, loading, failed }: { value?: TierTPS; loading: boolean; failed: boolean }) {
  const number = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });
  const status = loading ? "读取中…" : failed ? "读取失败" : "暂无 TPS 样本";
  return <div className="channel-tier-tps muted" title={value
    ? `请求档位 · ${value.samples} 个成功样本 · 输出 token 数 ÷ 请求总耗时`
    : status}>
    {value ? <><span>p50 {number(value.p50)}</span><span>p95 {number(value.p95)}</span><span>TPS</span></>
      : <span>{loading || failed ? status : "—"}</span>}
  </div>;
}
