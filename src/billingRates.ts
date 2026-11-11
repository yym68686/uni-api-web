import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";
import { boundGroups } from "./sub2apiImports";

export interface ObservedRate {
  rate: number;
  observed_at: number;
}

const validRate = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

export const siteRateCoefficient = (value?: number) =>
  typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 1;

export function adjustedSiteRate(value: number | null | undefined, coefficient?: number) {
  if (value == null) return value;
  const factor = siteRateCoefficient(coefficient);
  if (factor === 1 || !Number.isFinite(value)) return value;
  // Match the backend's decimal multiplication without binary float tails in
  // labels or threshold comparisons (e.g. 0.3 × 0.1 must be exactly 0.03).
  const parts = (number: number) => {
    const [mantissa, exponent = "0"] = number.toString().split("e");
    const [whole, fraction = ""] = mantissa.split(".");
    return { digits: BigInt(whole + fraction), exponent: Number(exponent) - fraction.length };
  };
  const a = parts(value), b = parts(factor);
  return Number(`${a.digits * b.digits}e${a.exponent + b.exponent}`);
}

export function matchesBillingRateFilter(
  billing: ReturnType<typeof billingRate>,
  filter: string,
) {
  if (!filter) return true;
  if (filter === "mismatch") return billing.mismatch;
  return billing.rate != null && billing.rate <= Number(filter);
}

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
  const coefficient = siteRateCoefficient(target?.rate_coefficient);
  return {
    rate: rate == null ? null : adjustedSiteRate(rate, coefficient)!,
    minimum: minimum == null ? null : adjustedSiteRate(minimum, coefficient)!,
    advertised: advertised == null ? null : adjustedSiteRate(advertised, coefficient)!,
    coefficient,
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
