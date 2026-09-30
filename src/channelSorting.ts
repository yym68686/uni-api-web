import type { Channel } from "./types";
import type { ChannelCheck } from "./ChannelChecks";
import { providerId } from "./format";

export const channelSortFields = [
  { value: "quality", label: "最近检测是否降智", direction: "asc" },
  { value: "probability", label: "不降智概率", direction: "desc" },
  { value: "multiplier", label: "倍率", direction: "asc" },
  { value: "latency", label: "首字延迟 P50", direction: "asc" },
  { value: "cache", label: "缓存率", direction: "desc" },
  { value: "success", label: "成功率", direction: "desc" },
  { value: "wait", label: "请求前等待 P50", direction: "asc" },
  { value: "status", label: "状态", direction: "asc" },
] as const;
export type ChannelSortField = (typeof channelSortFields)[number]["value"];
export interface ChannelSortRule {
  field: ChannelSortField;
  direction: "asc" | "desc";
}
export const qualityFirstRules: ChannelSortRule[] = [
  { field: "quality", direction: "asc" },
  { field: "probability", direction: "desc" },
];

export function parseChannelSortRules(value: string): ChannelSortRule[] {
  try {
    const data: unknown = JSON.parse(value);
    if (!Array.isArray(data)) return [];
    const used = new Set<string>();
    return data
      .filter((rule): rule is ChannelSortRule => {
        if (
          !rule ||
          !channelSortFields.some((f) => f.value === rule.field) ||
          !["asc", "desc"].includes(rule.direction) ||
          used.has(rule.field)
        )
          return false;
        used.add(rule.field);
        return true;
      })
      .map(({ field, direction }) => ({ field, direction }));
  } catch {
    return [];
  }
}

export function activeChannelSortRules(
  sort: string,
  saved: string,
): ChannelSortRule[] {
  if (sort === "custom") return parseChannelSortRules(saved);
  if (sort === "success") return [{ field: "success", direction: "desc" }];
  if (sort === "latency" || sort === "wait")
    return [{ field: sort, direction: "asc" }];
  return [];
}

export interface ChannelSortContext {
  checks: Map<string, ChannelCheck>;
  multipliers?: Record<string, Record<string, number | null>>;
}

function sortValue(
  row: Channel,
  field: ChannelSortField,
  context: ChannelSortContext,
): number | null {
  const check = context.checks.get(providerId(row));
  switch (field) {
    case "status":
      // Cooldown is transient; group it with available channels rather than
      // treating every ineligible channel as intentionally disabled.
      if (row.reason === "temporarily_disabled") return 1;
      return row.eligible ||
        row.reason === "eligible" ||
        row.reason === "channel_cooldown"
        ? 0
        : null;
    case "quality":
      return check?.verdict === "pass"
        ? 0
        : check?.verdict === "fail"
          ? 1
          : null;
    case "probability":
      return check?.history?.successful
        ? check.history.passed / check.history.successful
        : null;
    case "multiplier":
      return context.multipliers?.[row.source_id || ""]?.[row.provider] ?? null;
    case "latency":
      return row.stats?.response_created?.p50_ms ?? null;
    case "wait":
      return row.stats?.request_to_dispatch?.p50_ms ?? null;
    case "cache":
      return row.stats?.cache_rate ?? null;
    case "success":
      return row.stats?.success_rate ?? null;
  }
}

// Compare later rules only within ties from earlier rules. Missing data stays
// last for each rule in either direction; equal rows retain configuration order.
export function sortChannels(
  rows: Channel[],
  rules: ChannelSortRule[],
  context: ChannelSortContext,
): Channel[] {
  if (!rules.length) return rows;
  return rows
    .map((row, index) => ({
      row,
      index,
      values: rules.map((rule) => sortValue(row, rule.field, context)),
    }))
    .sort((a, b) => {
      for (let i = 0; i < rules.length; i++) {
        const av = a.values[i],
          bv = b.values[i];
        const am = av == null || !Number.isFinite(av),
          bm = bv == null || !Number.isFinite(bv);
        if (am !== bm) return am ? 1 : -1;
        if (am || bm) continue;
        const delta = av! - bv!;
        if (delta) return rules[i].direction === "asc" ? delta : -delta;
      }
      return a.index - b.index;
    })
    .map((item) => item.row);
}
