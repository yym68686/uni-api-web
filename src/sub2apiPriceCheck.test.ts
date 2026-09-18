import { expect, it } from "vitest";
import { assessPrice, pricePair } from "./sub2apiPriceCheck";
import type { SubUsage } from "./sub2apiPriceCheck";
import type { SubModelCheck } from "./sub2apiResults";
import type { ModelPrice } from "./types";

const price: ModelPrice = { model: "gpt-6-astra", input: 5, output: 30, cache_read: .5, cache_write: 0, cache_write_1h: 0, verified: true };
function check(usage: Partial<SubUsage>): SubModelCheck {
  return { model: price.model, state: "done", message: "", result: { model: price.model, checked_at: 1, verdict: "pass", availability: { status: "success", text: "test", ttft_ms: 1, duration_ms: 1, usage: { status: "matched", input_tokens: 100, output_tokens: 10, input_price: 5, output_price: 30, rate_multiplier: .1, paid_input_price: .5, paid_output_price: 3, ...usage } as SubUsage }, quality: { status: "skipped", text: "", ttft_ms: null, duration_ms: 0 } } };
}
it("compares pre-multiplier ledger prices, detects both undercharges and overcharges, and follows current settings", () => {
  expect(assessPrice(check({}), [price]).status).toBe("normal");
  expect(assessPrice(check({ input_price: 4 }), [price]).status).toBe("abnormal");
  expect(assessPrice(check({ output_price: 31 }), [price]).status).toBe("abnormal");
  expect(assessPrice(check({}), [{ ...price, input: 10 }]).status).toBe("abnormal");
  expect(assessPrice(check({ rate_multiplier: 0, paid_input_price: 0, paid_output_price: 0 }), [price]).status).toBe("normal");
  expect(pricePair(5, 30)).toBe("$5/$30");
});
it("does not call missing, pending, zero-token or unverified samples normal", () => {
  expect(assessPrice(check({ status: "pending" }), [price]).status).toBe("pending");
  expect(assessPrice(check({ status: "missing" }), [price]).status).toBe("unknown");
  expect(assessPrice(check({ input_tokens: 0, input_price: null }), [price]).status).toBe("unknown");
  expect(assessPrice(check({}), [{ ...price, verified: false }]).status).toBe("unpriced");
  expect(assessPrice(check({ input_price: null, output_price: 35 }), [price]).status).toBe("abnormal");
  expect(assessPrice(check({ input_price: 0 }), [price]).status).toBe("abnormal");
});
it("allows only the cost rounding quantum for very short probes", () => {
  expect(assessPrice(check({ output_tokens: 3, output_price: 30 + .00001 }), [price]).status).toBe("normal");
  expect(assessPrice(check({ output_tokens: 3, output_price: 30 + .001 }), [price]).status).toBe("abnormal");
});
