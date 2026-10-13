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
  };
}
