import { providerId } from "./format";
import type { Balance, Channel } from "./types";

export function balanceThresholdState(balance: Balance | undefined, threshold: string): "below" | "above" | "unknown" {
  const amount = Number(threshold);
  if (!threshold || !Number.isFinite(amount) || amount < 0 ||
      balance?.status !== "complete" || !balance.keys?.length ||
      (balance.key_count != null && balance.keys.length !== balance.key_count) ||
      balance.omitted_keys ||
      balance.keys.some(key => key.status !== "ok" || (!key.unlimited && (
        (!!key.currency && key.currency !== "USD") ||
        typeof key.amount !== "number" || !Number.isFinite(key.amount)
      )))) return "unknown";
  return balance.keys.every(key => !key.unlimited && key.amount! < amount) ? "below" : "above";
}

export function balanceBelowThreshold(
  balance: Balance | undefined,
  threshold: string,
) {
  return !threshold || balanceThresholdState(balance, threshold) === "below";
}

export interface BalanceChannelRank {
  model: string;
  rank: number;
}

export function rankBalanceProviders(rows: Channel[], topN: string) {
  const limit = Number(topN);
  const max = Number.isInteger(limit) && limit > 0 ? limit : Infinity;
  const modelScopes = new Map<
    string,
    { model: string; rows: { row: Channel; index: number }[] }
  >();

  rows.forEach((row, index) => {
    const scope = row.model;
    const group = modelScopes.get(scope) || { model: row.model, rows: [] };
    group.rows.push({ row, index });
    modelScopes.set(scope, group);
  });

  const providers: string[] = [];
  const seenProviders = new Set<string>();
  const ranks = new Map<string, BalanceChannelRank[]>();

  for (const { model, rows: group } of modelScopes.values()) {
    group.sort(
      (a, b) =>
        (a.row.position ?? Infinity) - (b.row.position ?? Infinity) ||
        a.index - b.index,
    );
    const modelProviders = new Set<string>();
    for (const { row } of group) {
      const id = providerId(row);
      if (modelProviders.has(id)) continue;
      modelProviders.add(id);
      const rank = modelProviders.size;
      if (rank > max) break;
      if (!seenProviders.has(id)) {
        providers.push(id);
        seenProviders.add(id);
      }
      const entries = ranks.get(id) || [];
      entries.push({ model, rank });
      ranks.set(id, entries);
    }
  }

  return { providers, ranks };
}
