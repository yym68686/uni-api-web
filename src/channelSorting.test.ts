import { expect, it } from "vitest";
import type { Channel } from "./types";
import type { ChannelCheck } from "./ChannelChecks";
import { providerId } from "./format";
import {
  activeChannelSortRules,
  parseChannelSortRules,
  qualityFirstRules,
  sortChannels,
} from "./channelSorting";

const row = (
  provider: string,
  stats: Record<string, unknown> = {},
  source_id = "a",
) =>
  ({
    provider,
    source_id,
    model: "gpt-6-sol",
    stats,
  }) as unknown as Channel;
const names = (rows: Channel[]) => rows.map((r) => r.provider);
it("groups latest quality first and then sorts probability within both groups, in either direction", () => {
  const rows = [
    row("fail-low"),
    row("pass-low"),
    row("fail-high"),
    row("pass-high"),
    row("unknown"),
  ];
  const checks = new Map(
    rows.slice(0, 4).map((r) => [
      providerId(r),
      {
        verdict: r.provider.startsWith("pass") ? "pass" : "fail",
        history: {
          total: 12,
          successful: 10,
          passed: r.provider.endsWith("high") ? 9 : 2,
        },
      } as ChannelCheck,
    ]),
  );
  expect(names(sortChannels(rows, qualityFirstRules, { checks }))).toEqual([
    "pass-high",
    "pass-low",
    "fail-high",
    "fail-low",
    "unknown",
  ]);
  expect(
    names(
      sortChannels(
        rows,
        [
          { field: "quality", direction: "desc" },
          { field: "probability", direction: "asc" },
        ],
        { checks },
      ),
    ),
  ).toEqual(["fail-low", "fail-high", "pass-low", "pass-high", "unknown"]);
  expect(names(rows)).toEqual([
    "fail-low",
    "pass-low",
    "fail-high",
    "pass-high",
    "unknown",
  ]);
});

it("uses channel metrics, zero values and source-specific billing, while missing data stays last", () => {
  const rows = [
    row("a", {
      success_rate: 0.9,
      cache_rate: 0.8,
      response_created: { p50_ms: 8000 },
    }),
    row("b", {
      success_rate: 0,
      cache_rate: 0,
      response_created: { p50_ms: 0 },
    }),
    row("c"),
    row("d", { success_rate: NaN }),
  ];
  const context = {
    checks: new Map(),
    multipliers: { a: { a: 0.2, b: 0 }, other: { a: 0.01 } },
  };
  for (const field of ["success", "cache", "latency", "multiplier"] as const) {
    expect(
      names(sortChannels(rows, [{ field, direction: "asc" }], context)),
    ).toEqual(["b", "a", "c", "d"]);
    expect(
      names(sortChannels(rows, [{ field, direction: "desc" }], context)),
    ).toEqual(["a", "b", "c", "d"]);
  }
  expect(
    names(
      sortChannels(
        [row("a"), row("a", {}, "other"), row("b")],
        [{ field: "multiplier", direction: "asc" }],
        context,
      ),
    ),
  ).toEqual(["b", "a", "a"]);
  const result = sortChannels(
    [row("a"), row("a", {}, "other")],
    [{ field: "multiplier", direction: "asc" }],
    context,
  );
  expect(result[0].source_id).toBe("other");
});

it("honors arbitrary rule priority and retains configuration order for exact ties", () => {
  const rows = [
    row("a", { success_rate: 0.9, cache_rate: 0.1 }),
    row("b", { success_rate: 0.8, cache_rate: 0.9 }),
    row("c", { success_rate: 0.8, cache_rate: 0.9 }),
  ];
  const rules = [
    { field: "success", direction: "desc" },
    { field: "cache", direction: "desc" },
  ] as const;
  expect(names(sortChannels(rows, [...rules], { checks: new Map() }))).toEqual([
    "a",
    "b",
    "c",
  ]);
  expect(
    names(sortChannels(rows, [...rules].reverse(), { checks: new Map() })),
  ).toEqual(["b", "c", "a"]);
  expect(sortChannels(rows, [], { checks: new Map() })).toBe(rows);
});

it("validates persisted rules and migrates existing quick sorts", () => {
  expect(parseChannelSortRules("invalid")).toEqual([]);
  expect(parseChannelSortRules('{"field":"cache"}')).toEqual([]);
  expect(
    parseChannelSortRules(
      JSON.stringify([
        null,
        { field: "quality", direction: "asc" },
        { field: "quality", direction: "desc" },
        { field: "invalid", direction: "asc" },
        { field: "success", direction: "wrong" },
      ]),
    ),
  ).toEqual([{ field: "quality", direction: "asc" }]);
  expect(activeChannelSortRules("success", "")).toEqual([
    { field: "success", direction: "desc" },
  ]);
  expect(activeChannelSortRules("latency", "")).toEqual([
    { field: "latency", direction: "asc" },
  ]);
  expect(
    activeChannelSortRules("custom", JSON.stringify(qualityFirstRules)),
  ).toEqual(qualityFirstRules);
});
