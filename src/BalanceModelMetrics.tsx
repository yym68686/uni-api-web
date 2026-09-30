import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { Connection, Metrics, Distribution } from "./types";
import { count, ms, rate } from "./format";
import { LatencyBadge } from "./LatencyBadge";
import { Tip } from "./ui";

type Timing = Pick<
  Distribution,
  "sample_count" | "p50_ms" | "p95_ms" | "mean_ms"
>;
export interface BalanceModelMetric {
  source_id: string;
  provider: string;
  model: string;
  success: number;
  failed: number;
  cancelled: number;
  success_rate: number | null;
  first_text: Timing;
  response_created: Timing;
  duration: Timing;
}
export const balanceModelMetricID = (
  source: string,
  provider: string,
  model: string,
) => JSON.stringify([source, provider, model]);

export function useBalanceModelMetrics(
  connection: Connection,
  scope: {
    range: string;
    model: string;
    keyId: string;
    to?: number;
    refresh: number;
    auto: boolean;
    enabled: boolean;
  },
) {
  const split = scope.keyId.indexOf("::");
  const source =
    split >= 0 ? scope.keyId.slice(0, split) : connection.sourceId || "";
  const key = split >= 0 ? scope.keyId.slice(split + 2) : scope.keyId;
  const params = new URLSearchParams({
    range: scope.range,
    model: scope.model,
    source_id: source,
    key_id: key,
  });
  if (scope.to) params.set("to", String(scope.to));
  const query = useQuery({
    queryKey: [
      "balance-model-metrics",
      connection.base,
      connection.session,
      params.toString(),
      scope.refresh,
    ],
    queryFn: async ({ signal }) => {
      if (
        split >= 0 &&
        (!source ||
          !key ||
          (connection.sourceId && connection.sourceId !== source))
      )
        throw new Error("所选 API key 不属于当前来源。");
      const result = await analyticsRequest<{
        data: BalanceModelMetric[];
        import?: Metrics["import"];
      }>(connection, "/analytics/v1/channel-model-stats?" + params, signal);
      if (!Array.isArray(result.data)) throw new Error("模型统计数据无效。");
      return result;
    },
    enabled: scope.enabled,
    retry: false,
    staleTime: 30000,
    refetchInterval: (q) =>
      initializationRetryInterval(q) || (scope.auto ? 30000 : false),
    refetchIntervalInBackground: false,
  });
  const byModel = useMemo(
    () =>
      new Map(
        (query.data?.data || []).map((item) => [
          balanceModelMetricID(
            connection.account ? item.source_id : "",
            item.provider,
            item.model,
          ),
          item,
        ]),
      ),
    [query.data, connection.account],
  );
  return { ...query, byModel };
}

function TimingCell({
  value,
  label,
  created,
}: {
  value?: Timing;
  label: string;
  created?: Timing;
}) {
  return (
    <Tip
      text={
        <>
          {label}：P50 {ms(value?.p50_ms)} · P95 {ms(value?.p95_ms)}
          <br />
          平均 {ms(value?.mean_ms)} · {count(value?.sample_count || 0)}{" "}
          个有效样本
          <br />
          {created && (
            <>
              响应创建 P50 {ms(created.p50_ms)}，与首个正文分别统计。
              <br />
            </>
          )}
          仅统计本渠道已完成尝试，不包含前序重试和派发前等待；取消、跳过及缺失时间不计入。
        </>
      }
    >
      <span className="balance-model-timing">
        {label === "首字延迟" ? (
          <LatencyBadge value={value?.p50_ms} />
        ) : (
          ms(value?.p50_ms)
        )}
        <small>
          {value?.sample_count ? `${count(value.sample_count)} 样本` : "未记录"}
        </small>
      </span>
    </Tip>
  );
}

export function BalanceModelMetricCells({
  value,
  pending,
  error,
  partial,
}: {
  value?: BalanceModelMetric;
  pending: boolean;
  error: boolean;
  partial: boolean;
}) {
  if (!value && (pending || error))
    return (
      <>
        {[0, 1, 2].map((i) => (
          <td key={i} className="muted balance-model-stat">
            {error ? "统计不可用" : "统计中…"}
          </td>
        ))}
      </>
    );
  const denominator = (value?.success || 0) + (value?.failed || 0);
  return (
    <>
      <td className="balance-model-stat">
        <Tip
          text={
            <>
              本渠道、本模型在所选时间范围内：成功 {count(value?.success || 0)}{" "}
              次，失败 {count(value?.failed || 0)}{" "}
              次。每次渠道尝试独立计数；取消 {count(value?.cancelled || 0)}{" "}
              次不计入分母。其他渠道重试成功不会计成本渠道成功。
              {partial && "历史仍在同步，当前仅包含已同步记录。"}
              {error && "刷新失败，显示上次统计。"}
            </>
          }
        >
          <span className="balance-model-rate">
            {rate(value?.success_rate)}
            <small>
              {denominator
                ? `${count(value!.success)}/${count(denominator)}`
                : "暂无请求"}
              {partial && denominator > 0 ? " · 部分" : ""}
            </small>
          </span>
        </Tip>
      </td>
      <td className="balance-model-stat">
        <TimingCell
          value={value?.first_text}
          created={value?.response_created}
          label="首字延迟"
        />
      </td>
      <td className="balance-model-stat">
        <TimingCell value={value?.duration} label="总耗时" />
      </td>
    </>
  );
}
