import { afterEach, expect, it, vi } from "vitest";
import { mergeSummary, readSummary } from "./summaryFeed";
import { summaryAccounts } from "./sub2apiAccounts";

afterEach(() => vi.unstubAllGlobals());
const full = {
  format: "summary-v1" as const,
  version: "v1",
  base_version: "",
  data: [
    { id: "a", value: 1 },
    { id: "b", value: 2 },
  ],
  removed: [],
  order: ["b", "a"],
};
it("merges updated and deleted rows without losing unchanged rows or order", () => {
  const first = mergeSummary(undefined, full);
  expect(first.rows.map((r) => r.value)).toEqual([2, 1]);
  const next = mergeSummary(first, {
    ...full,
    version: "v2",
    base_version: "v1",
    data: [
      { id: "a", value: 3 },
      { id: "c", value: 4 },
    ],
    removed: ["b"],
    order: ["a", "c"],
  });
  expect(next.rows).toEqual([
    { id: "a", value: 3 },
    { id: "c", value: 4 },
  ]);
  expect(first.rows.map((r) => r.value)).toEqual([2, 1]);
  expect(() => mergeSummary(next, { ...full, base_version: "v1" })).toThrow(
    "数据版本",
  );
  expect(() => mergeSummary(first, { ...full, order: ["a", "a"] })).toThrow(
    "列表数据不完整",
  );
  expect(() => mergeSummary(first, { ...full, order: ["a"] })).toThrow(
    "列表数据不完整",
  );
  expect(mergeSummary(next, { ...full, version: "new-server" }).rows).toEqual(
    first.rows,
  );
});
it("reuses a 304 and supports an older server during deployment", async () => {
  const previous = mergeSummary(undefined, full);
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(new Response(null, { status: 304 }))
    .mockResolvedValueOnce(Response.json({ data: ["legacy"] }));
  vi.stubGlobal("fetch", fetcher);
  const controller = new AbortController();
  expect(
    (await readSummary("/test", previous, controller.signal)).summary,
  ).toBe(previous);
  expect(fetcher.mock.calls[0][1].headers["X-Console-Since"]).toBe("v1");
  expect(
    (await readSummary("/test", undefined, controller.signal)).legacy,
  ).toEqual({ data: ["legacy"] });
  controller.abort();
  expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
});
it("retains the timeout when the query supplies its own cancellation signal", async () => {
  const timeout = new AbortController();
  const spy = vi.spyOn(AbortSignal, "timeout").mockReturnValue(timeout.signal);
  const fetcher = vi.fn(async (_url, init) => {
    timeout.abort();
    expect(init.signal.aborted).toBe(true);
    throw init.signal.reason;
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(
    readSummary("/test", undefined, new AbortController().signal),
  ).rejects.toBeDefined();
  expect(spy).toHaveBeenCalledWith(30000);
  spy.mockRestore();
});
it("reconstructs complete ordered account and model lists from independent rows", () => {
  const result = summaryAccounts({
    version: "v1",
    rows: [
      {
        id: "group",
        value: {
          account_id: "a",
          target: {
            group_id: 1,
            models: [{ model: "x", state: "done" }],
          } as any,
          position: 0,
        },
      },
      {
        id: "b",
        value: { account: { id: "b", targets: [] } as any, position: 1 },
      },
      {
        id: "a",
        value: { account: { id: "a", targets: [] } as any, position: 0 },
      },
    ],
  });
  expect(result.map((a) => a.id)).toEqual(["a", "b"]);
  expect(result[0].targets[0].models?.[0].model).toBe("x");
  expect(result[0].targets[0].details_omitted).toBe(true);
});
