import { analyticsRequest } from "./api";
import type { Connection, Metrics } from "./types";
export async function readMetrics(
  connection: Connection,
  path: string,
  signal: AbortSignal,
  endpoint: string,
  stream: string,
) {
  const source = new URLSearchParams(path.split("?")[1] || "");
  const range = source.get("window") || "15m";
  const keySource = source.get("api_key_id")?.split("::");
  if (connection.sourceId) source.set("source_id", connection.sourceId);
  else if (keySource && keySource.length > 1)
    source.set("source_id", keySource[0]);
  source.delete("window");
  source.set("range", range);
  source.delete("api_key_id");
  source.set("endpoint", endpoint);
  source.set("stream", stream);
  if (path.includes("timeseries")) source.set("timeseries", "true");
  const result = await analyticsRequest<Metrics>(
    connection,
    "/analytics/v1/analytics?" + source.toString(),
    signal,
  );
  if (!result || !result.total || !Array.isArray(result.data))
    throw new Error("分析服务返回了无效统计数据。");
  if (!connection.account) {
    result.data = result.data.map((row) => ({ ...row, source_id: undefined }));
    result.source_freshness = result.source_freshness?.map((item) => ({
      ...item,
      source_id: "",
    }));
  }
  return {
    ...result,
    window_minutes:
      range === "24h"
        ? 1440
        : range === "7d"
          ? 10080
          : range === "30d"
            ? 43200
            : range === "today"
              ? 1440
              : range === "week"
                ? 10080
                : range === "month"
                  ? 43200
                  : range === "year"
                    ? 525600
                    : 0,
    coverage: result.coverage || "partial",
    statistics_scope: "s3",
  };
}
