import { channelProfit, channelProfitCost } from "./ChannelProfit";
import { providerId, rowId } from "./format";
import { salePercent } from "./modelPrices";
import type { SubChannelSpendResult } from "./SubChannelSpend";
import type { Channel } from "./types";

const normalize = (amount: number) => Math.abs(amount) < 0.0000005 ? 0 : amount;
const yuan = new Intl.NumberFormat("en-US", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 6,
});

export const overviewProfitAmount = (amount: number) => `¥${yuan.format(amount)}`;

export interface OverviewChannelProfit {
  id: string;
  row: Channel;
  amount: number;
  provisional: boolean;
}

export function summarizeOverviewProfit(
  rows: Channel[],
  spend: ReadonlyMap<string, SubChannelSpendResult>,
  historyIncomplete: boolean,
) {
  const groups = new Map<string, OverviewChannelProfit & { incomplete: boolean }>();
  const seen = new Set<string>();
  let pending = false;
  let failed = false;
  for (const row of rows) {
    const key = rowId(row);
    if (seen.has(key)) continue;
    seen.add(key);
    const query = spend.get(key);
    const bill = query?.data;
    const stats = row.stats;
    if (!(stats.started > 0 || (stats.requests ?? 0) > 0 ||
      (stats.usage_samples ?? 0) > 0 || stats.estimated_cost_usd != null ||
      (bill && bill.status !== "no_records"))) continue;
    const id = providerId(row);
    const group = groups.get(id) ?? { id, row, amount: 0, provisional: false, incomplete: false };
    groups.set(id, group);
    pending ||= !!(query?.isPending || bill?.refreshing || bill?.status === "pending");
    failed ||= !!(query?.isError || bill?.sync_error || bill?.status === "error");
    const cost = channelProfitCost(bill);
    const usage = stats.usage_samples ?? 0;
    const unpriced = stats.priced_samples != null && stats.priced_samples < usage;
    const noRevenue = stats.requests === 0 && usage === 0;
    const profit = channelProfit(stats.estimated_cost_usd ?? (noRevenue ? 0 : null), cost?.actual,
      salePercent({ model: row.model, sale_percent: stats.sale_percent }));
    if (!row.provider || !profit || unpriced || cost?.upperBound) {
      group.incomplete = true;
    } else {
      group.amount += profit.amount;
      group.provisional ||= !!cost?.provisional;
    }
  }
  const usable = [...groups.values()].filter(group => !group.incomplete && Number.isFinite(group.amount));
  const total = usable.reduce((sum, group) => sum + group.amount, 0);
  const provisional = historyIncomplete || usable.some(group => group.provisional);
  const omitted = groups.size - usable.length;
  const ranked = usable.map(group => ({ ...group, amount: normalize(group.amount) }));
  const stable = (a: OverviewChannelProfit, b: OverviewChannelProfit) => a.id.localeCompare(b.id);
  return {
    amount: Number.isFinite(total) && (usable.length || (!groups.size && !historyIncomplete)) ? normalize(total) : null,
    counted: usable.length,
    omitted,
    pending,
    failed,
    provisional,
    gains: ranked.filter(group => group.amount > 0)
      .sort((a, b) => b.amount - a.amount || stable(a, b)).slice(0, 8),
    losses: ranked.filter(group => group.amount < 0)
      .sort((a, b) => a.amount - b.amount || stable(a, b)).slice(0, 8),
  };
}
