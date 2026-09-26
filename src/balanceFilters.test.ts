import { describe, expect, it } from "vitest";
import { balanceBelowThreshold, rankBalanceProviders } from "./balanceFilters";
import type { Balance, Channel } from "./types";

const balance = (amount: number): Balance => ({
  provider: "provider",
  status: "complete",
  key_count: 1,
  keys: [{ position: 1, status: "ok", amount, currency: "USD" }],
});

const row = (
  provider: string,
  model: string,
  position: number,
  source_id?: string,
): Channel =>
  ({
    source_id,
    provider,
    model,
    upstream_model: model,
    engine: "test",
    endpoint: "/v1/responses",
    stream: true,
    eligible: true,
    reason: "eligible",
    position,
    stats: {} as Channel["stats"],
  });

describe("balance filters", () => {
  it("treats the threshold as an exclusive USD upper bound, including zero", () => {
    expect(balanceBelowThreshold(balance(-1), "0")).toBe(true);
    expect(balanceBelowThreshold(balance(0), "0")).toBe(false);
    expect(balanceBelowThreshold(balance(9.99), "10")).toBe(true);
    expect(balanceBelowThreshold(balance(10), "10")).toBe(false);
    expect(balanceBelowThreshold(balance(10), "")).toBe(true);
  });

  it("selects top channels independently for each model", () => {
    const rows = [
      row("slow", "gpt-6-sol", 3),
      row("first", "gpt-6-sol", 1),
      row("second", "gpt-6-sol", 2),
      row("second", "gpt-5.5", 1),
      row("third", "gpt-5.5", 2),
    ];
    const result = rankBalanceProviders(rows, "2");
    expect(result.providers).toEqual([
      JSON.stringify(["", "first"]),
      JSON.stringify(["", "second"]),
      JSON.stringify(["", "third"]),
    ]);
    expect(result.ranks.get(JSON.stringify(["", "second"]))).toEqual([
      { model: "gpt-6-sol", rank: 2 },
      { model: "gpt-5.5", rank: 1 },
    ]);
  });

  it("limits a model to one top-N list across selected sources", () => {
    const rows = [
      row("one-first", "gpt-6-sol", 1, "source-one"),
      row("one-second", "gpt-6-sol", 2, "source-one"),
      row("two-first", "gpt-6-sol", 1, "source-two"),
      row("two-second", "gpt-6-sol", 2, "source-two"),
    ];
    expect(rankBalanceProviders(rows, "1").providers).toEqual([
      JSON.stringify(["source-one", "one-first"]),
    ]);
  });
});
