import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, KeyInfo, Metrics } from "./types";

export interface RequestOutcomes {
  requests: number;
  success: number;
  failed: number;
  success_rate: number | null;
}
export interface KeyRequestStatsResult {
  data: (RequestOutcomes & { source_id: string; key_id: string })[];
  total: RequestOutcomes;
  from: number;
  to: number;
  import?: Metrics["import"];
}
export interface KeyRequestScope {
  range: string;
  model: string;
  endpoint?: string;
  stream?: string;
}

export async function readKeyRequestStats(
  connection: Connection,
  scope: KeyRequestScope,
  signal: AbortSignal,
) {
  const params = new URLSearchParams({
    range: scope.range,
    model: scope.model,
    endpoint: scope.endpoint || "all",
    stream: scope.stream || "all",
  });
  if (connection.sourceId && connection.sourceId !== "all")
    params.set("source_id", connection.sourceId);
  const result = await analyticsRequest<KeyRequestStatsResult>(
    connection,
    "/analytics/v1/key-request-stats?" + params,
    signal,
  );
  if (!Array.isArray(result?.data) || !Number.isFinite(result.total?.requests))
    throw new Error("请求成功率数据无效。");
  return result;
}

export function requestRateLabel(stats?: RequestOutcomes, partial = false) {
  if (!stats?.requests) return partial ? "暂无已同步请求" : "暂无已完成请求";
  const rate = (stats.success / stats.requests) * 100;
  // Do not round a small nonzero failure count into a misleading 100%.
  const percent =
    rate > 99.9 && rate < 100
      ? "<100"
      : rate > 0 && rate < 0.1
        ? "<0.1"
        : rate.toFixed(1);
  return `请求成功率 ${percent}%（${stats.success}/${stats.requests}）${partial ? " · 部分记录" : ""}`;
}

export function useKeyRequestStats(
  connection: Connection,
  scope: KeyRequestScope,
  enabled: boolean,
  auto = false,
) {
  const query = useQuery({
    queryKey: [
      "key-request-stats",
      connection.session,
      connection.sourceId || "all",
      scope.range,
      scope.model,
      scope.endpoint || "all",
      scope.stream || "all",
    ],
    queryFn: ({ signal }) => readKeyRequestStats(connection, scope, signal),
    enabled,
    staleTime: 30_000,
    refetchInterval: (query) =>
      initializationRetryInterval(query) || (auto ? 30_000 : false),
    refetchIntervalInBackground: false,
  });
  const byKey = useMemo(() => {
    const values = new Map<string, RequestOutcomes>();
    for (const item of query.data?.data || []) {
      const id = connection.account
        ? `${item.source_id}::${item.key_id}`
        : item.key_id;
      const previous = values.get(id);
      values.set(
        id,
        previous
          ? {
              requests: previous.requests + item.requests,
              success: previous.success + item.success,
              failed: previous.failed + item.failed,
              success_rate: null,
            }
          : item,
      );
    }
    return values;
  }, [query.data, connection.account]);
  function label(key?: Pick<KeyInfo, "key_id" | "source_id">) {
    if (!query.data)
      return query.isError ? "请求成功率暂不可用" : "请求成功率加载中";
    const id =
      key &&
      (key.key_id.includes("::") || !connection.account
        ? key.key_id
        : `${key.source_id || connection.sourceId}::${key.key_id}`);
    const value = key ? byKey.get(id!) : query.data.total;
    const text = requestRateLabel(
      value,
      query.data.import?.caught_up === false,
    );
    return text + (query.isError ? " · 上次统计" : "");
  }
  return { ...query, label };
}

// Route browsers have no historical time/model controls. State the fixed
// window explicitly instead of inheriting hidden filters from another page.
export function useRouteKeyRequestStats(sourceId: string, enabled: boolean) {
  return useKeyRequestStats(
    {
      base: "",
      key: "",
      session: "account-route-browser",
      account: true,
      sourceId,
    },
    { range: "24h", model: "" },
    enabled && !!sourceId,
  );
}
