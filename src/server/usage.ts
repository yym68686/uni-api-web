import type { DailyUsagePoint, UsageResponse } from "@/lib/types";

function hashToSeed(input: string) {
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function mulberry32(seed: number) {
  let t = seed;
  return () => {
    t += 0x6d2b79f5;
    let x = t;
    x = Math.imul(x ^ (x >>> 15), x | 1);
    x ^= x + Math.imul(x ^ (x >>> 7), x | 61);
    return ((x ^ (x >>> 14)) >>> 0) / 4294967296;
  };
}

function clamp(n: number, min: number, max: number) {
  return Math.max(min, Math.min(max, n));
}

function isoDate(d: Date) {
  return d.toISOString().slice(0, 10);
}

export function getUsageData(): UsageResponse {
  const now = new Date();
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));

  const days = 14;
  const daily: DailyUsagePoint[] = [];
  const weekdayBoosts = [0.92, 0.98, 1.04, 1.07, 1.12, 1.02, 0.88] as const;

  for (let i = days - 1; i >= 0; i -= 1) {
    const day = new Date(today);
    day.setUTCDate(today.getUTCDate() - i);
    const date = isoDate(day);
    const rand = mulberry32(hashToSeed(`usage:${date}`));

    const weekdayBoost = weekdayBoosts[day.getUTCDay()] ?? 1;
    const base = 700 + rand() * 350;
    const requests = Math.round(base * weekdayBoost);

    const inputTokens = Math.round(requests * (260 + rand() * 120));
    const outputTokens = Math.round(requests * (420 + rand() * 220));
    const totalTokens = inputTokens + outputTokens;

    const errorRate = clamp(0.004 + rand() * 0.02, 0.003, 0.04);

    daily.push({
      date,
      requests,
      inputTokens,
      outputTokens,
      totalTokens,
      errorRate
    });
  }

  const latest = daily[daily.length - 1];
  if (!latest) {
    return {
      summary: { requests24h: 0, tokens24h: 0, errorRate24h: 0, spend24hUsd: 0 },
      daily: [],
      topModels: []
    };
  }
  const spend24hUsd = Number(((latest.totalTokens / 1000) * 0.008).toFixed(4));

  const topModels = [
    "gpt-4.1-mini",
    "gpt-4.1",
    "gpt-4o-mini",
    "claude-3.5-sonnet",
    "deepseek-chat"
  ].map((model, idx) => {
    const rand = mulberry32(hashToSeed(`model:${model}:${latest.date}`));
    const weight = 1 - idx * 0.12;
    const requests = Math.round(latest.requests * weight * (0.22 + rand() * 0.12));
    const tokens = Math.round((latest.totalTokens * weight * (0.18 + rand() * 0.16)) / 1000) * 1000;
    return { model, requests, tokens };
  });

  const totalModelRequests = topModels.reduce((acc, m) => acc + m.requests, 0);
  const normalized = topModels.map((m) => ({
    ...m,
    requests: Math.max(10, Math.round((m.requests / totalModelRequests) * latest.requests * 0.9))
  }));

  const summary = {
    requests24h: latest.requests,
    tokens24h: latest.totalTokens,
    errorRate24h: latest.errorRate,
    spend24hUsd
  };

  return {
    summary,
    daily,
    topModels: normalized
      .slice()
      .sort((a, b) => b.requests - a.requests)
      .slice(0, 5)
  };
}
