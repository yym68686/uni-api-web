import catalog from "../analytics-api/model_catalog.json";
import type { ModelPrice, PromptPriceTier } from "./types";

export const MODEL_PRICE_CATALOG = catalog;
const baseModels = catalog
  .map((price) => price.model)
  .sort((a, b) => b.length - a.length);

export function chargesCacheWrite(price: ModelPrice): boolean {
  return price.charge_cache_write ?? !/^(gpt|jev)-/i.test(price.model);
}

export function salePercent(
  price: Pick<ModelPrice, "model" | "sale_percent">,
): number {
  return (
    price.sale_percent ??
    (/^jev-/i.test(price.model)
      ? 100
      : /^(claude|gemini)-/i.test(price.model)
        ? 15
        : 2.5)
  );
}

export function canonicalPriceModel(model: string): string {
  return (
    baseModels.find((base) => model === base || model.startsWith(`${base}-`)) ||
    model
  );
}

export function displayedModelPrices(prices: ModelPrice[]): ModelPrice[] {
  const byModel = new Map(prices.map((price) => [price.model, price]));
  return catalog.map((reference) => {
    const price = byModel.get(reference.model);
    const placeholder =
      price?.source === "fact-discovered" &&
      !price.verified &&
      [
        price.input,
        price.output,
        price.cache_read,
        price.cache_write,
        price.cache_write_1h,
      ].every((value) => value === 0);
    return price && !placeholder
      ? price
      : {
          ...reference,
          charge_cache_write: price?.charge_cache_write,
          long_context_premium: price?.long_context_premium ?? false,
          sale_percent: price?.sale_percent,
        };
  });
}

// Catalog policy is shared with the backend, including for model suffixes.
export function promptPriceTier(model: string): PromptPriceTier | undefined {
  return catalog.find((entry) => entry.model === canonicalPriceModel(model))?.prompt_price_tier;
}

export function priceForPrompt(price: ModelPrice, inputTokens: number): ModelPrice {
  const tier = promptPriceTier(price.model);
  if (!tier || inputTokens <= tier.threshold_tokens) return price;
  return {
    ...price,
    input: price.input * tier.input_multiplier,
    output: price.output * tier.output_multiplier,
    cache_read: price.cache_read * tier.cache_multiplier,
    cache_write: price.cache_write * tier.cache_multiplier,
    cache_write_1h: price.cache_write_1h * tier.cache_multiplier,
  };
}
