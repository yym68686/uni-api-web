import { expect, it } from "vitest";
import {
  canonicalPriceModel,
  displayedModelPrices,
  MODEL_PRICE_CATALOG,
} from "./modelPrices";
import { SUB_MODELS } from "./sub2apiModels";
import { referencePrice } from "./sub2apiPriceCheck";

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
  expect(prices.filter((p) => !p.verified).map(p => p.model)).toEqual(["codex-auto-review", "gemini-3.1-pro"]);
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
  expect(canonicalPriceModel("claude-opus-5-5-thinking")).toBe("claude-opus-5-5");
  expect(canonicalPriceModel("gemini-3.1-pro-search")).toBe(base.model);
});
