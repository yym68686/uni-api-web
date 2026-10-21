import { afterEach, expect, it, vi } from "vitest";
import { readAccountData, type AccountData } from "./sub2apiAccounts";
import { mergeSummary } from "./summaryFeed";
import type { SubAccount } from "./Sub2apiChecks";

afterEach(() => vi.unstubAllGlobals());
const account = (id: string) =>
  ({ id, state: "idle", targets: [] }) as unknown as SubAccount;
const doc = (ids: string[], version = "v1") => ({
  format: "summary-v1" as const,
  version,
  base_version: "",
  removed: [],
  order: ids,
  data: ids.map((id, position) => ({
    id,
    value: { account: account(id), position },
  })),
});
it("bounds concurrency and publishes a complete ordered list with independent delta cursors", async () => {
  let active = 0,
    maximum = 0;
  const ids = ["c", "a", "b", "d"];
  const previous: AccountData = {
    data: [],
    accountSummaries: { a: mergeSummary(undefined, doc(["a"])) },
  };
  const fetched: string[] = [];
  let rateReads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit) => {
      expect(init.method).toBeUndefined();
      const url = new URL(input);
      const id = url.searchParams.get("account_id");
      if (new Headers(init.headers).get("X-Console-View") === "rates") {
        rateReads++;
        return Response.json(doc([]));
      }
      if (!id) {
        expect(new Headers(init.headers).get("X-Console-View")).toBe(
          "accounts",
        );
        return Response.json(doc(ids), {
          headers: { "X-Console-Account-Scope": "v2" },
        });
      }
      expect(url.searchParams.get("include_rates")).toBe("0");
      fetched.push(id);
      maximum = Math.max(maximum, ++active);
      await new Promise((resolve) => setTimeout(resolve, id === "c" ? 20 : 1));
      active--;
      if (id === "a") {
        expect(new Headers(init.headers).get("X-Console-Since")).toBe("v1");
        return new Response(null, { status: 304 });
      }
      return Response.json(doc([id], "v2"));
    }),
  );
  const result = await readAccountData(previous, new AbortController().signal);
  expect(maximum).toBe(2);
  expect(rateReads).toBe(1);
  expect(fetched.sort()).toEqual([...ids].sort());
  expect(result.data.map((a) => a.id)).toEqual(ids);
  expect(Object.keys(result.accountSummaries!).sort()).toEqual([...ids].sort());
  expect(previous.data).toEqual([]);
});
it("cancels sibling reads after a failed account without publishing partial data or changing old data", async () => {
  const previous: AccountData = { data: [account("old")] };
  const signals: AbortSignal[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit) => {
      const id = new URL(input).searchParams.get("account_id");
      if (!id)
        return Response.json(doc(["a", "b", "c"]), {
          headers: { "X-Console-Account-Scope": "v2" },
        });
      signals.push(init.signal!);
      if (id === "a") return new Response("unavailable", { status: 503 });
      return new Promise<Response>((_, reject) =>
        init.signal!.addEventListener(
          "abort",
          () => reject(init.signal!.reason),
          { once: true },
        ),
      );
    }),
  );
  await expect(
    readAccountData(previous, new AbortController().signal),
  ).rejects.toThrow("unavailable");
  expect(signals.every((s) => s.aborted)).toBe(true);
  expect(previous.data.map((a) => a.id)).toEqual(["old"]);
});
it("rejects a foreign or ignored account scope instead of merging it as complete", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) =>
      new URL(input).searchParams.has("account_id")
        ? Response.json(doc(["foreign"]))
        : Response.json(doc(["mine"]), {
            headers: { "X-Console-Account-Scope": "v2" },
          }),
    ),
  );
  await expect(
    readAccountData(undefined, new AbortController().signal),
  ).rejects.toThrow("账号数据不完整");
});
it("uses the original summary protocol when an older server does not advertise account scope", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init: RequestInit) => {
      const view = new Headers(init.headers).get("X-Console-View")!;
      calls.push(view);
      return Response.json(doc([view === "accounts" ? "header" : "complete"]));
    }),
  );
  expect(
    (await readAccountData(undefined, new AbortController().signal)).data.map(
      (a) => a.id,
    ),
  ).toEqual(["complete"]);
  expect(calls).toEqual(["accounts", "summary"]);
});

it("merges the single rate snapshot without mutating cursors and removes obsolete rates", async () => {
  const scoped = {
    ...doc(["a"]),
    order: ["a", "a:7"],
    data: [
      ...doc(["a"]).data,
      {
        id: "a:7",
        value: {
          account_id: "a",
          target: {
            group_id: 7,
            observed_rates: { stale: { rate: 8, observed_at: 1 } },
          },
          position: 0,
        },
      },
    ],
  };
  const requests: string[] = [];
  let failRates = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init: RequestInit) => {
      const view = new Headers(init.headers).get("X-Console-View")!;
      requests.push(view);
      if (view === "accounts")
        return Response.json(doc(["a"]), {
          headers: { "X-Console-Account-Scope": "v2" },
        });
      if (view === "rates") {
        if (failRates)
          return new Response("rates unavailable", { status: 503 });
        return Response.json({
          ...doc([]),
          order: ["a:7"],
          data: [
            { id: "a:7", value: { live: { rate: 0.2, observed_at: 42 } } },
          ],
        });
      }
      return Response.json(scoped);
    }),
  );
  const result = await readAccountData(undefined, new AbortController().signal);
  expect(result.data[0].targets[0].observed_rates).toEqual({
    live: { rate: 0.2, observed_at: 42 },
  });
  expect(
    result.accountSummaries!.a.rows[1].value.target!.observed_rates,
  ).toHaveProperty("stale");
  expect(requests).toEqual(["accounts", "summary", "rates"]);
  failRates = true;
  await expect(
    readAccountData(result, new AbortController().signal),
  ).rejects.toThrow("rates unavailable");
  expect(result.data[0].targets[0].observed_rates).toHaveProperty("live");
});
