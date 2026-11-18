import { describe, expect, it } from "vitest";
import { balanceIsKnown, balanceIsLow, formatBalance, walletStatusLabel, balanceTone, summarize } from "./format";
import { cleanBase, makeLimiter, request } from "./api";
import type { SiteWallet, Channel } from "./types";
const wallet = (amount: number | null): SiteWallet => ({
  account_id: "wallet",
  status: "ok",
  amount,
});
describe("metric meaning", () => {
  it("distinguishes a stale wallet balance from a missing value", () => {
    expect(walletStatusLabel({ ...wallet(12), status: "error" })).toBe("更新失败 · 上次余额");
    expect(walletStatusLabel({ ...wallet(null), status: "error" })).toBe("站点余额查询失败");
    expect(walletStatusLabel({ ...wallet(null), status: "missing" })).toBe("暂无站点余额");
    expect(formatBalance(0, "USD")).toBe("$0.00");
  });
  it("requires every linked wallet to have a verified nonpositive amount", () => {
    expect(balanceIsLow([wallet(0), wallet(-1)])).toBe(true);
    expect(balanceIsKnown([wallet(0), wallet(1)])).toBe(true);
    for (const data of [
      [],
      [wallet(-1), wallet(1)],
      [wallet(null)],
      [wallet(NaN)],
      [{ ...wallet(-1), status: "error" }],
      [{ ...wallet(-1), status: "pending" }],
    ])
      expect(balanceIsLow(data)).toBe(false);
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
