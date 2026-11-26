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
  const models = new Map<string, OverviewChannelProfit & { incomplete: boolean }>();
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
    // A public model can have multiple historical upstream mappings. Combine
    // those mappings, but never combine distinct models or sources on the list.
    const id = JSON.stringify([row.source_id || "", row.provider, row.model]);
    const group = models.get(id) ?? { id, row, amount: 0, provisional: false, incomplete: false };
    models.set(id, group);
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
  // Preserve the total's channel-level coverage: do not present a partially
  // reconciled channel as fully counted even if one of its models can rank.
  const channels = new Map<string, { amount: number; incomplete: boolean; provisional: boolean }>();
  const ranked: OverviewChannelProfit[] = [];
  for (const group of models.values()) {
    const id = providerId(group.row);
    const channel = channels.get(id) ?? { amount: 0, incomplete: false, provisional: false };
    channels.set(id, channel);
    const complete = !group.incomplete && Number.isFinite(group.amount);
    channel.incomplete ||= !complete;
    if (!complete) continue;
    channel.amount += group.amount;
    channel.provisional ||= group.provisional;
    ranked.push({ ...group, amount: normalize(group.amount) });
  }
  const usable = [...channels.values()].filter(channel => !channel.incomplete && Number.isFinite(channel.amount));
  const total = usable.reduce((sum, channel) => sum + channel.amount, 0);
  const provisional = historyIncomplete || usable.some(channel => channel.provisional);
  const omitted = channels.size - usable.length;
  const stable = (a: OverviewChannelProfit, b: OverviewChannelProfit) => a.id.localeCompare(b.id);
  return {
    amount: Number.isFinite(total) && (usable.length || (!channels.size && !historyIncomplete)) ? normalize(total) : null,
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
