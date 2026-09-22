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

it("merges public and private addresses for the same provider while retaining source definitions", () => {
  const first = channel(),
    second = channel({
      source_id: "do",
      source_name: "DigitalOcean",
      base: "http://gateway.cluster.local:8000",
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

it("does not merge distinct provider identifiers or engines even when display names match", () => {
  expect(
    groupManagedChannels([
      channel(),
      channel({ source_id: "a", provider: "other", name: "native" }),
      channel({ source_id: "b", engine: "claude" }),
    ]),
  ).toHaveLength(3);
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

it("uses the selected account's probe target when grouped sources have different account bindings", () => {
  const accounts = ["a", "b"].map((id) => ({
    id,
    name: id,
    base: `https://${id}.test`,
    email: "",
    state: "idle",
    message: "",
    synced_at: 0,
    targets: [
      {
        group_id: 1,
        name: id,
        channel: "native",
        platform: "openai",
        rate: 1,
        key_id: 1,
        active: true,
        state: "idle",
        message: "",
        result: null,
      },
    ],
  }));
  const channels = [
    channel({ account_id: "a", group_id: 1, account_ids: ["a"] }),
    channel({
      source_id: "do",
      account_id: "b",
      group_id: 1,
      account_ids: ["b"],
    }),
  ];
  const configured = managementRows(
    accounts,
    channels,
    [],
    "original",
    "b",
  ).find((r) => r.configured)!;
  expect(configured.account.id).toBe("b");
  expect(configured.target.group_id).toBe(1);
  expect(configured.configured?.members).toHaveLength(2);
  // Matching a site by URL alone cannot borrow the other account's probe target.
  channels[1] = { ...channels[1], account_id: "", group_id: 0 };
  const unbound = managementRows(accounts, channels, [], "original", "b").find(
    (r) => r.configured,
  )!;
  expect(unbound.account.id).toBe("");
  expect(unbound.target.active).toBe(false);
});
