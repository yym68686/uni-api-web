import catalog from "../analytics-api/model_catalog.json";
import type { ModelPrice } from "./types";

export const MODEL_PRICE_CATALOG = catalog;
const baseModels = catalog.map(price => price.model).sort((a, b) => b.length - a.length);

export function canonicalPriceModel(model: string): string {
  return baseModels.find(base => model === base || model.startsWith(`${base}-`)) || model;
}

export function displayedModelPrices(prices: ModelPrice[]): ModelPrice[] {
  const byModel = new Map(prices.map(price => [price.model, price]));
  return catalog.map(reference => {
    const price = byModel.get(reference.model);
    const placeholder = price?.source === "fact-discovered" && !price.verified &&
      [price.input, price.output, price.cache_read, price.cache_write, price.cache_write_1h].every(value => value === 0);
    return price && !placeholder ? price : reference;
  });
}
