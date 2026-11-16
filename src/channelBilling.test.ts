import { expect, it } from "vitest";
import { channelBilling, channelBillingHint } from "./channelBilling";
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

it("uses exact upstream model receipts in observation names without annotation or route mutation", () => {
  const observed = channelBilling(row, imports);
  expect(channelName(observed)).toBe("codex5-3");
  expect(observed.provider).toBe("opaque");
  expect(observed.billing_multiplier).toBe(3);
  expect(
    channelName(channelBilling({ ...row, upstream_model: "astra" }, imports)),
  ).toBe("codex5-0.3");
  expect(
    channelName(channelBilling({ ...row, upstream_model: "unknown" }, imports)),
  ).toBe("codex5-未知倍率");
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
  const copy = { ...row, provider: "sub2api-copy-01bd2c8136e0c116158d" };
  const result = channelBilling(copy, {
    data: [],
    labels: { fugue: { [copy.provider]: "bananarouter" } },
    model_billing: {},
    unavailable_sources: [],
  });
  expect(channelName(result)).toBe("bananarouter");
  expect(result.provider).toBe(copy.provider);
  expect(result.billing_multiplier).toBeNull();
  expect(result.billing_multiplier_source).toBeUndefined();
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
