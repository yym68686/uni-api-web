import { channelProfitCost } from "./ChannelProfit";
import type { SubChannelSpendResult } from "./SubChannelSpend";

// Aggregate only deductions attributed to the selected caller/time/model scope.
// Unused model rows must not hide other models' receipts; unresolved costs stay
// unknown and make the displayed sum a lower bound instead of an exact total.
export function balanceSpend(queries: (SubChannelSpendResult | undefined)[]) {
  const costs = queries.map(query => channelProfitCost(query?.data));
  const known = costs.filter((cost): cost is NonNullable<typeof cost> => !!cost);
  const failed = queries.some(query => query?.isError || query?.data?.status === "error" || query?.data?.sync_error);
  const pending = queries.some(query => query?.isPending || query?.data?.refreshing || query?.data?.status === "pending");
  const complete = queries.every((query, index) => (costs[index] && !costs[index].upperBound) || query?.data?.status === "no_records");
  return {
    actual: known.length ? known.reduce((sum, cost) => sum + cost.actual, 0) : null,
    upperBound: known.length > 0 && !complete,
    label: failed ? "查询失败" : pending ? "核对中" : queries.some(query => query?.data && query.data.status !== "no_records") ? "未完全核对" : "—",
    explanation: failed
      ? "账单读取失败，将自动重试；已有核对金额仍保留。未核对金额不按零处理。"
      : !complete
        ? "账单尚未全部核对。实际消费显示已核对金额的下限，利润显示上限；未核对金额不按零处理。"
        : "按当前时间、来源、调用 API key 和模型筛选汇总。无请求的模型不会清空其他模型的消费。",
  };
}
