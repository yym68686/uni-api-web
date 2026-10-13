import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";
import { boundGroups } from "./sub2apiImports";

export interface ObservedRate {
  rate: number;
  observed_at: number;
}

const validRate = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

export function billingRate(
  target?: SubTarget,
  model = "",
  configured?: ManagedChannel,
) {
  const groups = new Set(
    (configured?.members || (configured ? [configured] : []))
      .flatMap((member) => boundGroups(member))
      .map((group) => `${group.account_id}:${group.group_id}`),
  );
  const mixed = groups.size > 1;
  const advertised =
    !mixed && validRate(target?.billing?.rate) ? target.billing.rate : null;
  const upstream = configured?.model_mappings?.[model] || model;
  const allowedModels = configured
    ? new Set(
        configured.models.map(
          (name) => configured.model_mappings?.[name] || name,
        ),
      )
    : undefined;
  const observations = mixed
    ? []
    : Object.entries(target?.observed_rates || {})
        .filter(
          ([name, value]) =>
            validRate(value.rate) &&
            (upstream
              ? name === upstream
              : !allowedModels || allowedModels.has(name)),
        )
        .map(([, value]) => value);
  const rates = observations.map((value) => value.rate);
  const rate = rates.length ? Math.max(...rates) : advertised;
  const minimum = rates.length ? Math.min(...rates) : rate;
  return {
    rate,
    minimum,
    advertised,
    observed: rates.length > 0,
    mismatch:
      advertised != null &&
      rates.some(
        (value) =>
          Math.abs(value - advertised) >
          Math.max(1e-9, Math.abs(advertised) * 1e-9),
      ),
    observedAt: observations.length
      ? Math.min(...observations.map((value) => value.observed_at))
      : null,
  };
}
