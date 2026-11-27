import type { Channel } from "./types";
import type { SubImports } from "./sub2apiImports";
import { channelName, dateTime } from "./format";

// Only strip the suffix from API-generated billing labels. Configured names
// (including copies) may legitimately end in a hyphen and a number.
const billingSuffix = /-(?:\d+(?:\.\d+)?|未知倍率)$/;
const importedProvider = /^sub2api-[a-f0-9]{24}(?:--[\w-]+)?$/;

export function channelObservationName(row: Channel): string {
  return row.observation_name || channelName(row);
}

export function channelBilling(row: Channel, imports?: SubImports): Channel {
  const source = row.source_id || "";
  const observed =
    imports?.model_billing?.[source]?.[row.provider]?.[
      row.upstream_model || row.model
    ];
  const label = observed?.name ?? imports?.labels?.[source]?.[row.provider];
  const legacyRate = imports?.model_billing ? undefined : imports?.multipliers?.[source]?.[row.provider];
  return {
    ...row,
    provider_name: label,
    observation_name: observed || importedProvider.test(row.provider) ? label?.replace(billingSuffix, "") : undefined,
    billing_multiplier: observed
      ? observed.rate
      : imports?.model_billing
        ? null
        : legacyRate,
    billing_multiplier_source: observed?.rate_source ?? (legacyRate != null ? "advertised" : undefined),
    billing_multiplier_observed_at: observed?.observed_at,
  };
}

export const channelBillingMarker = {
  business: "账", observed: "测", advertised: "公", historical: "旧", mixed: "混",
} as const;

export function channelBillingHint(row: Channel): string | undefined {
  const at = row.billing_multiplier_observed_at;
  const date = at ? `账单时间：${dateTime(at)}。` : "";
  const value = row.billing_multiplier;
  switch (row.billing_multiplier_source) {
    case "business": return `近期业务账单倍率：${value}。${date}后续请求倍率可能变化。`;
    case "observed": return `检测令牌账单倍率：${value}。${date}非业务账单。`;
    case "advertised": return `渠道管理公布倍率：${value}（待核对）。仅用于显示与排序，不代表已核实的实际消费。`;
    case "historical": return `历史检测倍率：${value}。${date}没有近期账单或公布倍率，不代表当前业务倍率。`;
    case "mixed": return `倍率：${value}。各绑定的数值一致，但证据来源不同。`;
  }
}
