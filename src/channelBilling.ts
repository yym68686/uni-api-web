import type { Channel } from "./types";
import type { SubImports } from "./sub2apiImports";
import { channelName } from "./format";

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
  };
}

export function channelBillingHint(row: Channel): string | undefined {
  if (row.billing_multiplier_source !== "advertised") return undefined;
  return `渠道管理公布倍率：${row.billing_multiplier}（待核对）。仅用于显示与排序，不代表已核实的实际消费。`;
}
