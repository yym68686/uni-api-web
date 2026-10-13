import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { BillingRate } from "./BillingRate";
import { billingRate, matchesBillingRateFilter } from "./billingRates";
import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";

const target: SubTarget = {
  group_id: 33,
  name: "group",
  platform: "openai",
  channel: "opaque",
  rate: 0.3,
  key_id: 625,
  active: true,
  state: "idle",
  message: "",
  result: null,
  billing: { rate: 0.3, source: "key", checked_at: 1 },
  observed_rates: {
    luna: { rate: 3, observed_at: 100 },
    astra: { rate: 0.3, observed_at: 101 },
    free: { rate: 0, observed_at: 102 },
  },
};
const configured: ManagedChannel = {
  account_id: "a",
  account_ids: ["a"],
  group_id: 33,
  source_id: "s",
  source_name: "Fugue",
  api_key_id: "key",
  key_position: 1,
  key_prefix: "sk-",
  provider: "opaque",
  name: "channel",
  models: ["luna"],
  positions: {},
  revision: "1",
  manageable: true,
  engine: "gpt",
};

it("shows receipt rate with a discrepancy annotation only in management", () => {
  render(<BillingRate target={target} model="luna" />);
  expect(screen.getByText("3")).toBeVisible();
  expect(screen.getByText("倍率不一致 · 公布 0.3")).toBeVisible();
  expect(billingRate(target, "astra")).toMatchObject({
    rate: 0.3,
    mismatch: false,
    observed: true,
  });
  expect(billingRate(target, "free")).toMatchObject({
    rate: 0,
    mismatch: true,
    observed: true,
  });
});

it("keeps unknown models unverified and uses the highest observed rate for a mixed model group", () => {
  expect(billingRate(target, "missing")).toMatchObject({
    rate: 0.3,
    observed: false,
    mismatch: false,
  });
  expect(billingRate(target)).toMatchObject({
    minimum: 0,
    rate: 3,
    mismatch: true,
  });
  const mapped = {
    ...configured,
    models: ["public"],
    model_mappings: { public: "luna" },
  };
  expect(billingRate(target, "public", mapped)).toMatchObject({
    rate: 3,
    observed: true,
  });
});

it("filters only verified discrepancies for the selected model or any model", () => {
  expect(matchesBillingRateFilter(billingRate(target, "luna"), "mismatch")).toBe(true);
  expect(matchesBillingRateFilter(billingRate(target, "astra"), "mismatch")).toBe(false);
  expect(matchesBillingRateFilter(billingRate(target, "missing"), "mismatch")).toBe(false);
  expect(matchesBillingRateFilter(billingRate(target, "free"), "mismatch")).toBe(true);
  expect(matchesBillingRateFilter(billingRate(target), "mismatch")).toBe(true);
});

it("does not reuse one group's multiplier for a provider with multiple bound groups", () => {
  const configured = {
    kind: "configured",
    binding_status: "matched",
    models: ["luna"],
    bound_keys: [
      { account_id: "a", group_id: 1 },
      { account_id: "a", group_id: 2 },
    ],
  } as ManagedChannel;
  expect(billingRate(target, "luna", configured)).toMatchObject({
    rate: null,
    observed: false,
    advertised: null,
  });
});
