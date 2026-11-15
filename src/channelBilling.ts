import type { Channel } from "./types";
import type { SubImports } from "./sub2apiImports";

export function channelBilling(row: Channel, imports?: SubImports): Channel {
  const observed =
    imports?.model_billing?.[row.source_id || ""]?.[row.provider]?.[
      row.upstream_model || row.model
    ];
  return {
    ...row,
    provider_name:
      observed?.name ?? imports?.labels?.[row.source_id || ""]?.[row.provider],
    billing_multiplier: observed
      ? observed.rate
      : imports?.model_billing
        ? null
        : undefined,
    billing_multiplier_source: observed?.rate_source,
  };
}

export function channelBillingHint(row: Channel): string | undefined {
  if (row.billing_multiplier_source !== "advertised") return undefined;
  return `渠道管理公布倍率：${row.billing_multiplier}（待核对）。仅用于显示与排序，不代表已核实的实际消费。`;
}
