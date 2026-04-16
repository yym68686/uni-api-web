import rawDefaultModelPricing from "../../apps/api/app/data/default-model-pricing.json";

interface RawDefaultModelPricingEntry {
  inputUsdPerM: string | null;
  outputUsdPerM: string | null;
  discount?: string | null;
}

interface RawDefaultModelPricingMap {
  [prefix: string]: RawDefaultModelPricingEntry;
}

export interface DefaultModelPricingEntry {
  prefix: string;
  inputUsdPerM: string | null;
  outputUsdPerM: string | null;
  inputUsdPerMOriginal: string | null;
  outputUsdPerMOriginal: string | null;
  discount: number | null;
}

const DECIMAL_SCALE = 1_000_000;

const defaultModelPricingSource = rawDefaultModelPricing as RawDefaultModelPricingMap;

export const LANDING_MODEL_PRICING_PREFIXES = [
  "gpt-5.4",
  "claude-sonnet-4-6",
  "gemini-2.5-flash",
  "deepseek-chat"
] as const;

function parseDecimalToScaledInt(value: string | null | undefined) {
  if (!value) return null;

  const normalized = value.trim();
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) {
    throw new Error(`Invalid decimal value: ${value}`);
  }

  const [wholePart, fractionalPart = ""] = normalized.split(".");
  const whole = Number(wholePart);
  const fraction = Number((fractionalPart + "000000").slice(0, 6));

  return whole * DECIMAL_SCALE + fraction;
}

function formatScaledIntAsDecimal(value: number | null) {
  if (value === null) return null;

  const whole = Math.trunc(value / DECIMAL_SCALE);
  const fraction = String(value % DECIMAL_SCALE).padStart(6, "0").replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : `${whole}`;
}

function applyDiscount(value: string | null, discount: string | null | undefined) {
  const micros = parseDecimalToScaledInt(value);
  if (micros === null) return null;

  const ratio = parseDecimalToScaledInt(discount ?? null);
  if (ratio === null) return micros;

  return Math.floor((micros * ratio) / DECIMAL_SCALE);
}

export function getDefaultModelPricingEntry(prefix: string): DefaultModelPricingEntry | null {
  const rawEntry = defaultModelPricingSource[prefix];
  if (!rawEntry) return null;

  return {
    prefix,
    inputUsdPerM: formatScaledIntAsDecimal(applyDiscount(rawEntry.inputUsdPerM, rawEntry.discount)),
    outputUsdPerM: formatScaledIntAsDecimal(applyDiscount(rawEntry.outputUsdPerM, rawEntry.discount)),
    inputUsdPerMOriginal: formatScaledIntAsDecimal(parseDecimalToScaledInt(rawEntry.inputUsdPerM)),
    outputUsdPerMOriginal: formatScaledIntAsDecimal(parseDecimalToScaledInt(rawEntry.outputUsdPerM)),
    discount: rawEntry.discount ? Number(rawEntry.discount) : null
  };
}

export function getLandingModelPricingEntries() {
  return LANDING_MODEL_PRICING_PREFIXES.map((prefix) => getDefaultModelPricingEntry(prefix)).filter(
    (entry): entry is DefaultModelPricingEntry => entry !== null
  );
}
