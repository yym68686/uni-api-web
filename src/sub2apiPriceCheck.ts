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
export function referencePrice(model: string, prices?: ModelPrice[]) {
  return prices?.find(price => price.model === model && price.verified && known(price.input) && known(price.output));
}
export function assessPrice(check: SubModelCheck, prices?: ModelPrice[]) {
  const usage = check.result?.availability.usage;
  const expected = referencePrice(check.model, prices);
  if (!check.result) return { status: "untested", label: "未检测", usage, expected } as const;
  if (usage?.status === "pending") return { status: "pending", label: "核验中", usage, expected } as const;
  if (usage?.status !== "matched") return { status: "unknown", label: "未确认", usage, expected } as const;
  if (!expected) return { status: "unpriced", label: "参考价未确认", usage, expected } as const;
  // sub2api stores component costs to 10 decimal places. A short "say test"
  // response can round a correct unit price: compare within its cost quantum.
  const different = (actual: number | null, value: number, tokens: number | null) =>
    known(actual) && Math.abs(actual - value) > Math.max(1e-6, 0.00005 / Math.max(1, tokens || 0)) + Math.abs(value) * 1e-9;
  if (different(usage.input_price, expected.input, usage.input_tokens) || different(usage.output_price, expected.output, usage.output_tokens))
    return { status: "abnormal", label: "异常", usage, expected } as const;
  if (!known(usage.input_price) || !known(usage.output_price)) return { status: "unknown", label: "样本不足", usage, expected } as const;
  return { status: "normal", label: "正常", usage, expected } as const;
}

export function dollars(value: number | null | undefined, digits = 10) {
  return known(value) ? `$${value.toLocaleString("en-US", { maximumFractionDigits: digits })}` : "—";
}
export function pricePair(input: number | null | undefined, output: number | null | undefined) {
  return `${dollars(input, 6)}/${dollars(output, 6)}`;
}
