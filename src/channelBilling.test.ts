import { expect, it } from "vitest";
import { channelBilling, channelBillingHint, channelObservationName } from "./channelBilling";
import { channelName } from "./format";
import { sortChannels } from "./channelSorting";
import type { Channel } from "./types";
import type { SubImports } from "./sub2apiImports";

const row = {
  source_id: "fugue",
  provider: "opaque",
  model: "alias",
  upstream_model: "luna",
} as Channel;
const imports: SubImports = {
  data: [],
  labels: { fugue: { opaque: "codex5-0.3" } },
  unavailable_sources: [],
  model_billing: {
    fugue: {
      opaque: {
        luna: { name: "codex5-3", rate: 3 },
        astra: { name: "codex5-0.3", rate: 0.3 },
        unknown: { name: "codex5-未知倍率", rate: null },
      },
    },
  },
};

it("separates model-specific multipliers from observation names without changing existing labels or route identity", () => {
  const observed = channelBilling(row, imports);
  expect(channelName(observed)).toBe("codex5-3");
  expect(channelObservationName(observed)).toBe("codex5");
  expect(observed.provider).toBe("opaque");
  expect(observed.billing_multiplier).toBe(3);
  expect(
    channelName(channelBilling({ ...row, upstream_model: "astra" }, imports)),
  ).toBe("codex5-0.3");
  expect(
    channelName(channelBilling({ ...row, upstream_model: "unknown" }, imports)),
  ).toBe("codex5-未知倍率");
  expect(channelObservationName(channelBilling({ ...row, upstream_model: "unknown" }, imports))).toBe("codex5");
  expect(
    channelBilling({ ...row, source_id: "other" }, imports).billing_multiplier,
  ).toBeNull();
});

it("sorts model-specific multipliers without guessing missing metadata from provider-wide rates", () => {
  const expensive = channelBilling(row, imports);
  const cheap = { ...row, provider: "cheap", billing_multiplier: 1 };
  const unknown = { ...row, provider: "unverified", billing_multiplier: null };
  const context = {
    checks: new Map(),
    multipliers: { fugue: { opaque: 0.3, cheap: 1, unverified: 0.01 } },
  };
  expect(
    sortChannels(
      [expensive, unknown, cheap],
      [{ field: "multiplier", direction: "asc" }],
      context,
    ).map((value) => value.provider),
  ).toEqual(["cheap", "opaque", "unverified"]);
  expect(
    channelBilling(row, { ...imports, model_billing: undefined }).provider_name,
  ).toBe("codex5-0.3");
});

it("shows an unbound copy's source label without changing route identity or inventing a rate", () => {
  const copy = { ...row, provider: "sub2api-copy-4ce253a5f4587683da99" };
  const listing = {
    data: [],
    labels: { fugue: { [copy.provider]: "openai-next-2026" } },
    model_billing: {},
    unavailable_sources: [],
  };
  const result = channelBilling(copy, listing);
  expect(channelName(result)).toBe("openai-next-2026");
  expect(channelObservationName(result)).toBe("openai-next-2026");
  expect(channelName(channelBilling({ ...copy, source_id: "other" }, listing))).toBe(copy.provider);
  expect(result.provider).toBe(copy.provider);
  expect(result.billing_multiplier).toBeNull();
  expect(result.billing_multiplier_source).toBeUndefined();
});

it("uses provider-wide rates only with legacy data and strips only the generated suffix", () => {
  const provider = "sub2api-1234567890abcdef12345678--responses";
  const imported = { ...row, provider };
  const metadata = {
    data: [],
    labels: { fugue: { [provider]: "渠道-2026--0.13" } },
    multipliers: { fugue: { [provider]: 0.13 } },
    unavailable_sources: [],
  };
  const legacy = channelBilling(imported, metadata);
  expect(channelObservationName(legacy)).toBe("渠道-2026-");
  expect(legacy.billing_multiplier).toBe(0.13);
  expect(legacy.billing_multiplier_source).toBe("advertised");
  const current = channelBilling(imported, { ...metadata, model_billing: {} });
  expect(channelObservationName(current)).toBe("渠道-2026-");
  expect(current.billing_multiplier).toBeNull();
});

it("uses the backend's exact advertised fallback for display and sorting without changing actual spend", () => {
  const unverified = {
    ...row,
    stats: { actual_cost_usd: 4, actual_cost_samples: 2 },
  } as Channel;
  const result = channelBilling(unverified, {
    ...imports,
    model_billing: {
      fugue: {
        opaque: {
          luna: { name: "codex5-0.13", rate: 0.13, rate_source: "advertised" },
        },
      },
    },
  });
  expect(channelName(result)).toBe("codex5-0.13");
  expect(result.billing_multiplier).toBe(0.13);
  expect(result.billing_multiplier_source).toBe("advertised");
  expect(channelBillingHint(result)).toContain("0.13（待核对）");
  expect(result.stats).toBe(unverified.stats);
  expect(result.stats.actual_cost_usd).toBe(4);
  expect(channelBillingHint(channelBilling(row, imports))).toBeUndefined();
  expect(sortChannels(
    [{ ...row, provider: "expensive", billing_multiplier: 1 }, result],
    [{ field: "multiplier", direction: "asc" }],
    { checks: new Map() },
  ).map(value => value.provider)).toEqual(["opaque", "expensive"]);
});
