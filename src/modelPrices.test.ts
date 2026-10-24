import { expect, it } from "vitest";
import {
  canonicalPriceModel,
  displayedModelPrices,
  MODEL_PRICE_CATALOG,
} from "./modelPrices";
import { SUB_MODELS } from "./sub2apiModels";
import { referencePrice } from "./sub2apiPriceCheck";

it("keeps Kimi K3.1 Pro unconfirmed and independent from K3 or saved operator prices",()=>{
 const model="kimi-k3.1-pro";
 const p=MODEL_PRICE_CATALOG.find(p=>p.model===model)!;
 expect(p).toMatchObject({verified:false,input:0,output:0,cache_read:0,cache_write:0,cache_write_1h:0});
 expect(referencePrice(model,MODEL_PRICE_CATALOG)).toBeUndefined();
 expect(canonicalPriceModel(model+"-thinking")).toBe(model);
 const manual={...p,input:7,output:11,source:"manual",verified:true};
 expect(displayedModelPrices([manual]).find(p=>p.model===model)).toEqual(manual);
 expect(referencePrice(model,[manual])).toEqual(manual);
});

it("keeps DeepSeek Flash 1017 pricing independent of the previous Flash model", () => {
  const model = "deepseek-v4.1-flash-1017";
  const reference = MODEL_PRICE_CATALOG.find(p => p.model === model)!;
  expect(reference).toMatchObject({ input: .3, output: 1.2, cache_read: .006, cache_write: .3, cache_write_1h: .3, verified: true });
  expect(canonicalPriceModel(model + "-thinking")).toBe(model);
  const previous = { ...reference, model: "deepseek-v4.1-flash", input: 9, source: "manual" };
  const custom = { ...reference, input: 2, source: "manual", verified: false };
  expect(displayedModelPrices([previous, custom]).find(p => p.model === model)).toEqual(custom);
  expect(referencePrice(model, [previous, custom])).toBeUndefined();
  expect(referencePrice(model + "-thinking", [previous, reference])).toEqual(reference);
});

it("shows exactly the detection models, preserves manual values and fills old placeholders", () => {
  const base = MODEL_PRICE_CATALOG.find((p) => p.model === "gemini-3.1-pro")!;
  const prices = displayedModelPrices([
    { ...base, model: "gemini-3.1-pro-search", input: 999 },
    { ...base, model: "legacy-model", input: 888 },
    { ...base, input: 7, source: "manual", verified: false },
  ]);
  expect(prices.map((p) => p.model)).toEqual(
    MODEL_PRICE_CATALOG.map((p) => p.model),
  );
  expect(SUB_MODELS.some((model) => model.startsWith("jev-"))).toBe(false);
  expect(prices.find((p) => p.model === base.model)).toMatchObject({
    input: 7,
    verified: false,
  });
  expect(prices.filter((p) => !p.verified).map(p => p.model)).toEqual(["gpt-6.1-sol", "codex-auto-review", "kimi-k3.1-pro", "gemini-3.1-pro"]);
  const placeholder = {
    ...base,
    input: 0,
    output: 0,
    cache_read: 0,
    cache_write: 0,
    cache_write_1h: 0,
    verified: false,
    source: "fact-discovered",
  };
  expect(
    displayedModelPrices([placeholder]).find((p) => p.model === base.model),
  ).toMatchObject({ input: 2, output: 12, verified: true });
  expect(
    displayedModelPrices([]).find((p) => p.model === "codex-auto-review")
      ?.verified,
  ).toBe(false);
});

it("uses the longest complete model prefix, ignoring stale suffix prices and respecting unconfirmed bases", () => {
  const base = MODEL_PRICE_CATALOG.find((p) => p.model === "gemini-3.1-pro")!;
  const suffix = { ...base, model: "gemini-3.1-pro-search", input: 999 };
  expect(referencePrice(suffix.model, [suffix, base])).toEqual(base);
  expect(
    referencePrice(suffix.model, [suffix, { ...base, verified: false }]),
  ).toBeUndefined();
  expect(canonicalPriceModel("glm-5.3-flash-thinking")).toBe("glm-5.3-flash");
  expect(canonicalPriceModel("claude-fable-5-1-thinking")).toBe(
    "claude-fable-5-1",
  );
  expect(canonicalPriceModel("gpt-5.50")).toBe("gpt-5.50");
  expect(canonicalPriceModel("gpt-6.1-sol-thinking")).toBe("gpt-6.1-sol");
  expect(referencePrice("gpt-6.1-sol", MODEL_PRICE_CATALOG)).toBeUndefined();
  expect(canonicalPriceModel("claude-opus-5-5-thinking")).toBe("claude-opus-5-5");
  expect(canonicalPriceModel("claude-sonnet-5-5")).toBe("claude-sonnet-5-5");
  expect(canonicalPriceModel("claude-sonnet-5-5-thinking")).toBe("claude-sonnet-5-5");
  const sonnet55 = MODEL_PRICE_CATALOG.find(p => p.model === "claude-sonnet-5-5")!;
  const sonnet5 = { ...sonnet55, model: "claude-sonnet-5", input: 999 };
  expect(referencePrice("claude-sonnet-5-5-thinking", [sonnet5, sonnet55])).toEqual(sonnet55);
  expect(canonicalPriceModel("gemini-3.1-pro-search")).toBe(base.model);
});

it("includes the official Haiku rates without changing saved operator prices", () => {
  const reference = MODEL_PRICE_CATALOG.find(p => p.model === "claude-haiku-5-5")!;
  expect(reference).toMatchObject({ input: .1, output: .5, cache_read: .01, cache_write: .125, cache_write_1h: .2, verified: true, prompt_price_tier: { threshold_tokens: 100000, input_multiplier: 5, output_multiplier: 5, cache_multiplier: 5 } });
  const custom = { ...reference, input: .3, source: "manual", verified: false };
  expect(displayedModelPrices([custom]).find(p => p.model === custom.model)).toEqual(custom);
  expect(canonicalPriceModel("claude-haiku-5-5-thinking")).toBe("claude-haiku-5-5");
});

it("uses Gemini 4 Argon reference prices for aliases and preserves saved prices", () => {
  const reference = MODEL_PRICE_CATALOG.find(p => p.model === "gemini-4-argon")!;
  expect(reference).toMatchObject({ input: 2, output: 10, cache_read: .1, verified: true });
  expect(referencePrice("gemini-4-argon-high", MODEL_PRICE_CATALOG)).toEqual(reference);
  const custom = { ...reference, input: 4, output: 20, source: "manual", verified: false };
  expect(displayedModelPrices([custom]).find(p => p.model === custom.model)).toEqual(custom);
  expect(referencePrice(custom.model, [custom])).toBeUndefined();
});
