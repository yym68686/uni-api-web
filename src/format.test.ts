import { describe, expect, it } from "vitest";
import { balanceIsLow, balanceLabel, balanceTone, summarize } from "./format";
import { cleanBase, makeLimiter, request } from "./api";
import type { Balance, BalanceKey, Channel } from "./types";
const key = (amount: number | null): BalanceKey => ({
  status: "ok",
  position: 1,
  kind: "wallet",
  amount,
  currency: "USD",
});
const balance = (keys: BalanceKey[], extra = {}): Balance => ({
  provider: "test",
  status: "complete",
  keys,
  key_count: keys.length,
  ...extra,
});
describe("metric meaning", () => {
  it("requires all keys to be known and exhausted", () => {
    expect(balanceIsLow(balance([key(0), key(-1)]))).toBe(true);
    for (const data of [
      balance([key(-1), key(1)]),
      balance([key(null)]),
      balance([{ ...key(-1), status: "timeout" }]),
      balance([key(-1)], { key_count: 2, omitted_keys: 1 }),
      balance([{ ...key(-1), unlimited: true }]),
    ])
      expect(balanceIsLow(data)).toBe(false);
    expect(
      balanceIsLow(
        balance([
          {
            status: "ok",
            position: 1,
            kind: "key_rate_limits",
            windows: [{ window: "5h", remaining: 0 }],
          },
        ]),
      ),
    ).toBe(true);
    expect(balanceLabel(key(0))).toBe("$0.00");
    expect(balanceLabel(key(null))).toBe("上游未提供");
  });
  it("uses weighted counts rather than averaging success percentages", () => {
    const rows = [
      {
        provider: "a",
        eligible: true,
        stats: { success: 1, success_rate_denominator: 1 },
      },
      {
        provider: "b",
        eligible: false,
        stats: { success: 0, success_rate_denominator: 99 },
      },
    ] as Channel[];
    expect(summarize(rows)).toMatchObject({
      providers: 2,
      eligible: 1,
      completed: 100,
      successRate: 0.01,
    });
  });
  it("normalizes service roots and rejects credential-bearing URLs", () => {
    expect(cleanBase("https://example.com/prefix/v1/")).toBe(
      "https://example.com/prefix",
    );
    for (const url of [
      "javascript:alert(1)",
      "https://user:pass@example.com",
      "https://example.com?key=secret",
      "https://example.com/#secret",
    ])
      expect(() => cleanBase(url)).toThrow();
  });
  it("limits concurrency, cancels queued jobs, and releases permits after failure", async () => {
    const limited = makeLimiter(2),
      controller = new AbortController();
    let active = 0,
      max = 0,
      calls = 0;
    const task = () =>
      limited(async () => {
        calls++;
        active++;
        max = Math.max(max, active);
        await new Promise((r) => setTimeout(r, 5));
        active--;
        return 1;
      }, controller.signal);
    const jobs = [task(), task()];
    const abort = new AbortController();
    const cancelled = limited(async () => {
      throw new Error("must never run");
    }, abort.signal).catch((e) => e);
    abort.abort("cancelled");
    jobs.push(...Array.from({ length: 8 }, task));
    expect(await cancelled).toBe("cancelled");
    await Promise.all(jobs);
    expect(max).toBe(2);
    expect(calls).toBe(10);
    await expect(
      limited(async () => {
        throw new Error("failure");
      }, controller.signal),
    ).rejects.toThrow("failure");
    expect(await task()).toBe(1);
  });
  it("does not reflect potentially sensitive upstream error bodies", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response('{"error":"secret-key"}', { status: 500 });
    try {
      await expect(
        request(
          { base: "https://example.com", key: "private", session: "1" },
          "/v1/api-keys",
        ),
      ).rejects.toThrow("HTTP 500");
    } finally {
      globalThis.fetch = original;
    }
  });
});

it.each([
  [-1, "negative"],
  [-0.0001, "negative"],
  [0, "balance-warning"],
  [10, "balance-warning"],
  [50, "balance-warning"],
  [50.0001, "balance-positive"],
  [null, "muted"],
  [undefined, "muted"],
  [NaN, "muted"],
  [Infinity, "muted"],
] as const)("colors balance %s as %s", (amount, tone) =>
  expect(balanceTone(amount)).toBe(tone),
);
