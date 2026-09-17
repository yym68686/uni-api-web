import { expect, it } from "vitest";
import { catalogMetrics, emptyStats, ranges } from "./analytics";
import type { Catalog, Metrics } from "./types";
it("joins persisted statistics using channel identity while preserving key order and zero-sample routes", () => {
  const data = ["third", "first", "unused"].map((provider) => ({
    provider,
    model: "alias",
    upstream_model: "upstream",
    endpoint: "all",
    stream: null,
    stats: emptyStats(),
  }));
  const catalog = { data, snapshot_revision: "current" } as Catalog;
  const metrics = {
    data: [
      {
        ...data[1],
        stats: { ...emptyStats(), success: 7, success_rate_denominator: 7 },
      },
      { ...data[0], stats: { ...emptyStats(), failed: 4 } },
    ],
  } as Metrics;
  expect(
    catalogMetrics(catalog, metrics).map((row) => [
      row.provider,
      row.stats.success,
      row.stats.failed,
    ]),
  ).toEqual([
    ["third", 0, 4],
    ["first", 7, 0],
    ["unused", 0, 0],
  ]);
  expect(catalogMetrics(catalog, undefined)).toEqual([]);
});
it("calendar choices use calendar ranges rather than rolling durations", () => {
  expect(Object.fromEntries(ranges).week).toBe("本周");
  expect(Object.fromEntries(ranges).month).toBe("本月");
  expect(Object.fromEntries(ranges).today).toBe("今天");
});
it("warns only when a source has completed live traffic beyond its latest persisted fact", async () => {
  const { staleHistorySources } = await import("./analytics");
  const now = 1_800_000_000;
  const row = (source_id: string, completed: number, inflight = 0) => ({
    source_id,
    history_configured: true,
    stats: { ...emptyStats(), success_rate_denominator: completed, inflight },
  });
  const persisted = {
    source_freshness: [
      { source_id: "old", latest_fact_at: now - 900 },
      { source_id: "fresh", latest_fact_at: now - 10 },
    ],
  } as Metrics;
  const live = {
    generated_at: now,
    data: [
      row("old", 3),
      row("fresh", 3),
      row("idle", 0),
      row("waiting", 0, 2),
      row("empty-history", 1),
      { ...row("unconfigured", 1), history_configured: false },
    ],
  } as Metrics;
  expect([...staleHistorySources(persisted, live)]).toEqual([
    "old",
    "empty-history",
  ]);
  expect(staleHistorySources({} as Metrics, live).size).toBe(0);
  expect(staleHistorySources(persisted, undefined).size).toBe(0);
});
