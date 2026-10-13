import { expect, it } from "vitest";
import { channelBilling } from "./channelBilling";
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

it("sorts observed multipliers and leaves unverified channels last without using advertised fallback", () => {
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
