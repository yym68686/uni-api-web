import { canonicalPriceModel } from "./modelPrices";
import type { SubModelCheck } from "./sub2apiResults";
import type { ModelPrice } from "./types";

export interface SubUsage {
  status: "pending" | "matched" | "missing" | "unsupported" | "unavailable" | "ambiguous";
  message?: string;
  log_id?: number;
  request_id?: string;
  checked_at?: number;
  created_at?: string;
  actual_cost: number | null;
  total_cost: number | null;
  rate_multiplier: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  cache_read_tokens: number | null;
  cache_creation_tokens: number | null;
  input_price: number | null;
  output_price: number | null;
  cache_read_price: number | null;
  cache_write_price: number | null;
  paid_input_price: number | null;
  paid_output_price: number | null;
  duration_ms: number | null;
  first_token_ms: number | null;
}

const known = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value >= 0;
export function configuredPrice(model: string, prices?: ModelPrice[]) {
  return prices?.find(price => price.model === canonicalPriceModel(model) && known(price.input) && known(price.output) &&
    // Unpublished catalog/discovered prices use zero as a placeholder. An
    // explicitly saved manual zero remains a valid comparison price.
    (price.verified || price.source === "manual" || price.input > 0 || price.output > 0));
}
export function referencePrice(model: string, prices?: ModelPrice[]) {
  const price = configuredPrice(model, prices);
  return price?.verified ? price : undefined;
}
function compareUnitPrices(usage?: SubUsage, expected?: ModelPrice) {
  if (usage?.status !== "matched" || !expected) return "unknown";
  // sub2api stores component costs to 10 decimal places. A short "say test"
  // response can round a correct unit price: compare within its cost quantum.
  const different = (actual: number | null, value: number, tokens: number | null) =>
    known(actual) && Math.abs(actual - value) > Math.max(1e-6, 0.00005 / Math.max(1, tokens || 0)) + Math.abs(value) * 1e-9;
  if (different(usage.input_price, expected.input, usage.input_tokens) || different(usage.output_price, expected.output, usage.output_tokens))
    return "mismatch";
  if (!known(usage.input_price) || !known(usage.output_price)) return "unknown";
  return "match";
}
export function assessPrice(check: SubModelCheck, prices?: ModelPrice[]) {
  const usage = check.result?.availability.usage;
  const expected = configuredPrice(check.model, prices);
  const comparison = compareUnitPrices(usage, expected);
  const details = { usage, expected, comparison };
  if (!check.result) return { status: "untested", label: "未检测", ...details } as const;
  if (usage?.status === "pending") return { status: "pending", label: "核验中", ...details } as const;
  if (usage?.status !== "matched") return { status: "unknown", label: "未确认", ...details } as const;
  if (!expected?.verified) return { status: "unpriced", label: "参考价未确认", ...details } as const;
  if (comparison === "mismatch") return { status: "abnormal", label: "异常", ...details } as const;
  if (comparison === "unknown") return { status: "unknown", label: "样本不足", ...details } as const;
  return { status: "normal", label: "正常", ...details } as const;
}

export function priceFilterStatus(checks: SubModelCheck[], prices?: ModelPrice[]) {
  const statuses = checks.map(check => assessPrice(check, prices).status);
  if (statuses.includes("abnormal")) return "abnormal";
  if (statuses.length && statuses.every(status => status === "normal")) return "normal";
  return "unconfirmed";
}

export function matchesPriceFilter(filter: string, checks: SubModelCheck[], prices?: ModelPrice[]) {
  if (!filter) return true;
  if (filter === "unconfirmed_match" || filter === "unconfirmed_mismatch") {
    const comparison = filter === "unconfirmed_match" ? "match" : "mismatch";
    return checks.some(check => {
      const result = assessPrice(check, prices);
      return result.status !== "normal" && result.status !== "abnormal" && result.comparison === comparison;
    });
  }
  return priceFilterStatus(checks, prices) === filter;
}

export function dollars(value: number | null | undefined, digits = 10) {
  return known(value) ? `$${value.toLocaleString("en-US", { maximumFractionDigits: digits })}` : "—";
}
export function pricePair(input: number | null | undefined, output: number | null | undefined) {
  return `${dollars(input, 6)}/${dollars(output, 6)}`;
}
