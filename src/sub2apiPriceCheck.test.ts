import { expect, it } from "vitest";
import { assessPrice, matchesPriceFilter, pricePair } from "./sub2apiPriceCheck";
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

it("compares unverified reference prices without confirming them or treating missing data as a match", () => {
  const unverified = [{ ...price, verified: false }];
  const same = check({}), different = check({ input_price: 4 });
  expect(assessPrice(same, unverified)).toMatchObject({ status: "unpriced", comparison: "match" });
  expect(assessPrice(different, unverified)).toMatchObject({ status: "unpriced", comparison: "mismatch" });
  expect(matchesPriceFilter("unconfirmed_match", [same], unverified)).toBe(true);
  expect(matchesPriceFilter("unconfirmed_mismatch", [different], unverified)).toBe(true);
  expect(matchesPriceFilter("unconfirmed", [same, different], unverified)).toBe(true);
  expect(matchesPriceFilter("normal", [same], unverified)).toBe(false);
  expect(matchesPriceFilter("abnormal", [different], unverified)).toBe(false);
  expect(matchesPriceFilter("unconfirmed_match", [same], [price])).toBe(false);
  expect(matchesPriceFilter("unconfirmed_mismatch", [different], [price])).toBe(false);
  // All-model views include a channel if any unconfirmed model qualifies.
  expect(matchesPriceFilter("unconfirmed_match", [same, different], unverified)).toBe(true);
  expect(matchesPriceFilter("unconfirmed_mismatch", [same, different], unverified)).toBe(true);
  for (const data of [{ status: "pending" as const }, { status: "missing" as const }, { input_price: null }, { output_price: NaN }]) {
    expect(matchesPriceFilter("unconfirmed_match", [check(data)], unverified)).toBe(false);
    expect(matchesPriceFilter("unconfirmed_mismatch", [check(data)], unverified)).toBe(false);
  }
  expect(matchesPriceFilter("unconfirmed_match", [same], [])).toBe(false);
  expect(matchesPriceFilter("unconfirmed_mismatch", [same], [{ ...price, input: 0, output: 0, verified: false, source: "fact-discovered" }])).toBe(false);
  expect(matchesPriceFilter("unconfirmed_mismatch", [same], [{ ...price, input: 0, output: 0, verified: false, source: "official" }])).toBe(false);
  expect(matchesPriceFilter("unconfirmed_match", [check({input_price: 0, output_price: 0})], [{ ...price, input: 0, output: 0, verified: false, source: "manual" }])).toBe(true);
  expect(matchesPriceFilter("unconfirmed_match", [check({output_tokens: 3, output_price: 30.00001})], unverified)).toBe(true);
  expect(matchesPriceFilter("unconfirmed_mismatch", [check({input_price: null, output_price: 35})], unverified)).toBe(true);
  expect(matchesPriceFilter("unconfirmed_match", [{...same, model: "gpt-6-astra-search"}], unverified)).toBe(true);
});

it("assesses Haiku prices using per-request cache-inclusive input and the recorded prompt tier", () => {
  const haiku: ModelPrice = { model: "claude-haiku-5-5", input: .1, output: .5, cache_read: .01, cache_write: .125, cache_write_1h: .2, verified: true };
  const sample = (usage: Partial<SubUsage>) => ({ ...check({ input_tokens: 5000, cache_read_tokens: 80000, cache_creation_tokens: 15000, input_price: .1, output_price: .5, ...usage }), model: "claude-haiku-5-5-thinking" });
  expect(assessPrice(sample({}), [haiku]).status).toBe("normal");
  expect(assessPrice(sample({ input_tokens: 5001 }), [haiku]).status).toBe("abnormal");
  expect(assessPrice(sample({ input_tokens: 5001, input_price: .5, output_price: 2.5 }), [haiku]).status).toBe("normal");
  expect(assessPrice(sample({ input_tokens: 200000, input_price: .5, output_price: 2.5 }), [haiku]).expected).toMatchObject({ input: .5, output: 2.5, cache_write: .625, cache_write_1h: 1 });
  expect(assessPrice(sample({ cache_read_tokens: null }), [haiku]).status).toBe("unknown");
});
