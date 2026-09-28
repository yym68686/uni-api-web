import { describe, expect, it, vi } from "vitest";
import { balanceSite, makeBalanceScheduler } from "./balanceRequests";

const deferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
};

describe("balance scanning", () => {
  it("starts independent sites together while serializing all channels of one site", async () => {
    const schedule = makeBalanceScheduler();
    const signal = new AbortController().signal;
    const hold = deferred();
    const started: string[] = [];
    const job = (name: string) => async () => { started.push(name); await hold.promise; };
    const first = schedule(balanceSite("https://SITE.test/v1", "a"), job("first"), signal);
    const second = schedule(balanceSite("https://site.test/dashboard", "b"), job("second"), signal);
    const others = Array.from({ length: 8 }, (_, i) =>
      schedule(balanceSite(`https://site-${i}.test`, ""), job(`other-${i}`), signal),
    );
    await vi.waitFor(() => expect(started).toHaveLength(9));
    expect(started).not.toContain("second");
    hold.resolve();
    await Promise.all([first, second, ...others]);
    expect(started.at(-1)).toBe("second");
  });

  it("shares site exclusion between account and gateway queries without queue head blocking", async () => {
    const schedule = makeBalanceScheduler();
    const signal = new AbortController().signal;
    const hold = deferred();
    const started: string[] = [];
    const run = (site: string, name: string, gateway: boolean) => schedule(site, async () => {
      started.push(name);
      await hold.promise;
    }, signal, gateway ? "source" : undefined);
    const jobs = [run("same", "account", false), ...Array.from({ length: 5 }, (_, i) => run("same", `queued-${i}`, true)),
      run("other-1", "other-1", true), run("other-2", "other-2", true), run("other-3", "other-3", true)];
    await vi.waitFor(() => expect(started).toEqual(["account", "other-1", "other-2", "other-3"]));
    hold.resolve();
    await Promise.all(jobs);
    expect(started.slice(4)).toEqual(["queued-0", "queued-1", "queued-2", "queued-3", "queued-4"]);
  });

  it("does not send cancelled queued requests and continues after an upstream failure", async () => {
    const schedule = makeBalanceScheduler();
    const signal = new AbortController().signal;
    const cancelled = new AbortController();
    const hold = deferred();
    const first = schedule("site", async () => { await hold.promise; throw new Error("upstream timeout"); }, signal);
    const failed = expect(first).rejects.toThrow("upstream timeout");
    const never = vi.fn(async () => 0);
    const queued = schedule("site", never, cancelled.signal);
    const aborted = expect(queued).rejects.toBe("cancelled");
    cancelled.abort("cancelled");
    const next = schedule("site", async () => 42, signal);
    hold.resolve();
    await Promise.all([failed, aborted]);
    expect(await next).toBe(42);
    expect(never).not.toHaveBeenCalled();
    expect(await schedule("site", async () => 43, signal)).toBe(43);
  });

  it("respects each legacy gateway's capacity without making separate sources wait", async () => {
    const schedule = makeBalanceScheduler();
    const signal = new AbortController().signal;
    const hold = deferred();
    const started: string[] = [];
    const jobs = ["fugue", "digitalocean"].flatMap((source) =>
      Array.from({ length: 4 }, (_, i) => schedule(`${source}-${i}`, async () => {
        started.push(`${source}-${i}`);
        await hold.promise;
      }, signal, source)),
    );
    await vi.waitFor(() => expect(started).toHaveLength(6));
    expect(started).not.toContain("fugue-3");
    expect(started).not.toContain("digitalocean-3");
    hold.resolve();
    await Promise.all(jobs);
    expect(started).toHaveLength(8);
  });

  it("groups URL variants and uses source isolation when site metadata is missing", () => {
    expect(balanceSite("https://Example.com.:8443/prefix/v1", "a"))
      .toBe(balanceSite("http://example.com/dashboard", "b"));
    expect(balanceSite(undefined, "source-a")).toBe(balanceSite("invalid", "source-a"));
    expect(balanceSite(undefined, "source-a")).not.toBe(balanceSite(undefined, "source-b"));
  });
});
