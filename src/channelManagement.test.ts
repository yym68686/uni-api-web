import { expect, it } from "vitest";
import { groupManagedChannels, managementRows } from "./channelManagement";
import type { ManagedChannel } from "./channelManagement";
import { managedRouteCount } from "./channelRouteData";

const channel = (overrides: Partial<ManagedChannel> = {}): ManagedChannel => ({
  kind: "configured",
  provider: "native",
  name: "native",
  base: "https://upstream.test",
  engine: "gpt",
  source_id: "fugue",
  source_name: "Fugue",
  models: ["original"],
  account_ids: [],
  account_id: "",
  group_id: 0,
  api_key_id: "",
  key_position: 0,
  key_prefix: "",
  positions: {},
  revision: "",
  manageable: false,
  ...overrides,
});

it("merges sources by provider, normalized site and engine while retaining independent model definitions", () => {
  const first = channel(),
    second = channel({
      source_id: "do",
      source_name: "DigitalOcean",
      base: "https://upstream.test/",
      models: ["alias"],
      model_mappings: { alias: "real" },
      account_ids: ["account"],
    });
  const groups = groupManagedChannels([first, second]);
  expect(groups).toHaveLength(1);
  expect(groups[0]).toMatchObject({
    source_name: "Fugue / DigitalOcean",
    models: ["original", "alias"],
    account_ids: ["account"],
    members: [first, second],
  });
  const rows = managementRows([], [first, second], [], "alias");
  expect(rows).toHaveLength(1);
  expect(rows[0].accountIds).toEqual(["account"]);
  expect(rows[0].selected?.model).toBe("alias");
  expect(first.models).toEqual(["original"]);
  expect(second.model_mappings).toEqual({ alias: "real" });
});

it("does not merge different upstreams, tenant paths, engines or unknown addresses", () => {
  const channels = [
    channel(),
    channel({ source_id: "a", base: "https://other.test" }),
    channel({ source_id: "b", base: "https://upstream.test/tenant" }),
    channel({ source_id: "c", engine: "claude" }),
    channel({ source_id: "d", base: "" }),
    channel({ source_id: "e", base: "" }),
  ];
  expect(groupManagedChannels(channels)).toHaveLength(6);
});

it("counts a caller key separately in each source and marks failed or pending sources incomplete", () => {
  const group = groupManagedChannels([
    channel(),
    channel({ source_id: "do", source_name: "DigitalOcean" }),
  ])[0];
  const data = {
    data: [
      {
        provider: "native",
        model: "original",
        api_key_id: "same-id",
        key_position: 1,
        key_prefix: "masked",
        position: 1,
      },
      {
        provider: "native",
        model: "alias",
        api_key_id: "same-id",
        key_position: 1,
        key_prefix: "masked",
        position: 2,
      },
    ],
    unavailable_keys: [],
  };
  expect(
    managedRouteCount(group, ["fugue", "do"], [{ data }, { data }]),
  ).toEqual({ count: 2, partial: false });
  expect(managedRouteCount(group, ["fugue", "do"], [{ data }, {}])).toEqual({
    count: 1,
    partial: true,
  });
  expect(
    managedRouteCount(
      group,
      ["fugue", "do"],
      [{ data }, { data, isError: true }],
    ),
  ).toEqual({ count: 2, partial: true });
  expect(managedRouteCount(group, ["fugue", "do"], [{}, {}])).toBeNull();
});
