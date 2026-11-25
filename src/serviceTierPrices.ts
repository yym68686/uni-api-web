// Public reference rates, USD per million tokens. These are deliberately
// independent of the editable Standard rates and are not billing estimates.
// Verified 2026-11-26 against the official OpenAI and Claude pricing pages.
export type PriceTier = "fast" | "ultrafast";
type Rate = readonly [input: number, output: number, cacheRead: number, cacheWrite?: number, cacheWriteLong?: number];
const openAI = "https://developers.openai.com/api/docs/pricing";
const claude = "https://platform.claude.com/docs/en/build-with-claude/fast-mode";
const promotionalEnd = Date.parse("2027-03-01T00:00:00-08:00");
type TierReference = {
  fast: Rate;
  ultrafast?: Rate;
  promoted?: Rate;
  source: string;
  expires?: number;
  note?: string;
};

const references: Record<string, TierReference> = {
  "gpt-6.2-astra": { fast: [16, 80, .8, 20, 40], ultrafast: [48, 240, 2.4, 60, 120], promoted: [24, 120, 1.2, 30, 60], source: openAI },
  "gpt-6.2-sol": { fast: [4, 20, .2, 5, 10], ultrafast: [12, 60, .6, 15, 30], promoted: [6, 30, .3, 7.5, 15], source: openAI },
  "gpt-6-astra": { fast: [20, 100, 2, 25, 50], ultrafast: [60, 300, 6, 75, 150], source: openAI },
  "gpt-6.1-sol": { fast: [4, 20, .2, 5, 10], ultrafast: [12, 60, .6, 15, 30], source: openAI },
  "claude-fable-5-2": { fast: [16, 80, .4, 20, 32], source: claude },
  "claude-opus-5-5": { fast: [8, 40, .4, 10, 16], source: claude },
  "claude-opus-5": { fast: [10, 50, 1, 12.5, 20], source: claude, note: "研究预览" },
  "claude-opus-4-8": { fast: [10, 50, 1, 12.5, 20], source: claude, expires: Date.parse("2026-12-01T00:00:00Z"), note: "2026-11-30 停止支持 Fast，以官方公告为准" },
};

export function serviceTierPrice(model: string, tier: PriceTier, now = Date.now()) {
  const reference = references[model];
  if (!reference || (reference.expires && now >= reference.expires)) return undefined;
  const promotional = tier === "ultrafast" && !!reference.promoted && now < promotionalEnd;
  const rate = tier === "fast" ? reference.fast : promotional ? reference.promoted : reference.ultrafast;
  if (!rate) return undefined;
  return {
    rate,
    source: reference.source,
    note: promotional ? "优惠至 2027-02-28（太平洋时间），3 月 1 日起恢复常规 Ultrafast 价" : reference.note,
    cacheDuration: reference.source === openAI ? ["30 分钟", "12 小时"] : ["5 分钟", "1 小时"],
  };
}

export function tierPriceText(rate: Rate) {
  return rate.slice(0, 3).map(value => `$${value}`).join(" / ");
}
