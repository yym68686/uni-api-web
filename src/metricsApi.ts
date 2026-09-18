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
  const selectedKey = source.get("api_key_id") || "";
  const separator = selectedKey.indexOf("::");
  const keySource = separator < 0 ? "" : selectedKey.slice(0, separator);
  const keyId = separator < 0 ? selectedKey : selectedKey.slice(separator + 2);
  const selectedSource =
    connection.sourceId === "all" ? "" : connection.sourceId;
  if (keySource && selectedSource && keySource !== selectedSource)
    throw new Error("所选 API key 不属于当前来源，请重新选择。");
  if (separator >= 0 && (!keySource || !keyId))
    throw new Error("API key 筛选无效，请重新选择。");
  if (keySource || selectedSource)
    source.set("source_id", keySource || selectedSource!);
  // The catalog API uses api_key_id; analytics stores the same fingerprint
  // under key_id. Preserve both the owning source and the caller identity.
  if (keyId) source.set("key_id", keyId);
  else source.delete("key_id");
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
      /^(?:[1-9]|1[0-9]|2[0-4])h$/.test(range)
        ? Number.parseInt(range) * 60
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
