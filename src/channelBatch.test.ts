import { afterEach, expect, it, vi } from "vitest";
import {
  applyChannelBatch,
  batchTargetSettings,
  prepareChannelBatch,
  projectBatchPositions,
} from "./channelBatch";
import type {
  BatchBinding,
  BatchDraft,
  BatchPart,
  BatchProgress,
} from "./channelBatch";
import type { InstalledChannel } from "./sub2apiImports";
import type { ManagedChannel } from "./channelManagement";

afterEach(() => vi.unstubAllGlobals());
const installed = (source: string, key = "k1"): InstalledChannel => ({
  source_id: source,
  source_name: source === "a" ? "Fugue" : "DigitalOcean",
  api_key_id: key,
  key_position: key === "k1" ? 1 : 2,
  key_prefix: "masked",
  provider: `site-${key}`,
  name: "site",
  account_id: "acct",
  group_id: 1,
  models: ["old", "alias"],
  model_mappings: { alias: "old" },
  positions: { old: 2, alias: 1 },
  revision: "r1",
  manageable: true,
});
const native = (source: string): ManagedChannel => ({
  ...installed(source),
  provider: "native",
  name: "native",
  kind: "configured",
  account_ids: [],
  engine: "gpt",
  models: ["old", "new"],
  model_mappings: {},
  api_key_id: "",
});
const draft = (part: BatchPart = "all", configured = false): BatchDraft => ({
  part,
  name: "Fixture",
  scope: configured
    ? { kind: "configured", source: "a", provider: "native" }
    : { kind: "site", account: "acct", group: 1 },
  originals: { new: "new" },
  aliases: { renamed: "old" },
  models: { new: "new", renamed: "old" },
  positions: { new: 3, renamed: 2 },
  anchor: {
    source: "a",
    key: "k1",
    provider: configured ? "native" : "site-k1",
    revision: "r1",
  },
});
const binding: BatchBinding = {
  id: "a:k1",
  source: "a",
  sourceName: "Fugue",
  key: "k1",
  keyPosition: 1,
  name: "site",
  provider: "site-k1",
  installed: installed("a"),
  current: { old: "old", alias: "old" },
};
const options = {
  revision: "r1",
  manageable: true,
  batch_revisions: true,
  channels: [
    { provider: "peer", model: "old" },
    { provider: "site-k1", model: "old" },
    { provider: "site-k1", model: "alias" },
  ],
};

it("applies model selection alone without changing existing aliases or their positions", () => {
  const result = batchTargetSettings(draft("models"), binding, options);
  expect(result.models).toEqual({ new: "new", alias: "old" });
  expect(result.positions).toEqual({ new: 1, alias: 1 });
});
it("applies aliases alone and supports clearing aliases without changing originals or their positions", () => {
  expect(batchTargetSettings(draft("aliases"), binding, options)).toMatchObject(
    {
      models: { old: "old", renamed: "old" },
      positions: { old: 2, renamed: 1 },
    },
  );
  expect(
    batchTargetSettings({ ...draft("aliases"), aliases: {} }, binding, options)
      .models,
  ).toEqual({ old: "old" });
});
it("positions never add or remove models; only shared public names are moved", () => {
  const d = { ...draft("positions"), positions: { old: 1, unknown: 5 } };
  expect(batchTargetSettings(d, binding, options)).toMatchObject({
    models: binding.current,
    positions: { old: 1, alias: 1 },
    clamped: false,
  });
  expect(
    batchTargetSettings(draft("positions"), binding, options).skip,
  ).toContain("无对应模型");
});
it("all settings replace models and aliases, clamp positions, and reject partial-name collisions", () => {
  expect(batchTargetSettings(draft(), binding, options)).toMatchObject({
    models: { new: "new", renamed: "old" },
    positions: { new: 1, renamed: 1 },
    clamped: true,
  });
  expect(() =>
    batchTargetSettings(
      { ...draft("models"), originals: { alias: "alias" } },
      binding,
      options,
    ),
  ).toThrow("冲突");
  expect(() =>
    batchTargetSettings(
      { ...draft("all"), originals: {}, aliases: {} },
      binding,
      options,
    ),
  ).toThrow("至少一个模型");
});

function fixture(configured = false, missing = false) {
  const writes: { path: string; method: string; body: any }[] = [],
    revisions: Record<string, string> = { a: "r1", b: "r1" };
  const site = [
    installed("a"),
    installed("a", "k2"),
    installed("b"),
    { ...installed("b", "k2"), group_id: 99 },
  ];
  const channels = [
    native("a"),
    native("b"),
    { ...native("c"), engine: "claude" },
  ];
  const fetch = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input),
      source = url.searchParams.get("source_id") || url.pathname.split("/")[4];
    if (init?.method && init.method !== "GET") {
      const body = JSON.parse(String(init.body)),
        id = body.source_id || source;
      writes.push({ path: url.pathname, method: init.method, body });
      if (body.revision !== revisions[id])
        return new Response("版本变化", { status: 409 });
      revisions[id] = `r${Number(revisions[id].slice(1)) + 1}`;
      return Response.json({ revision: revisions[id] });
    }
    if (url.pathname.endsWith("/channel-management"))
      return Response.json({
        data: channels,
        unavailable_sources: missing ? ["DigitalOcean"] : [],
      });
    if (url.pathname.endsWith("/sub2api/channels"))
      return Response.json({
        data: configured ? [] : site,
        unavailable_sources: [],
      });
    if (url.pathname.endsWith("/channel-routes"))
      return Response.json({
        revision: revisions[source],
        manageable: true,
        batch_revisions: true,
        snapshot_consistent: true,
        data: ["k1", "k2"].flatMap((key) => {
          const provider = configured
            ? key === "k1"
              ? "native"
              : "copy"
            : `site-${key}`;
          return [
            {
              provider: "peer",
              api_key_id: key,
              key_position: key === "k1" ? 1 : 2,
              model: "old",
              upstream_model: "old",
              position: 1,
            },
            {
              provider,
              origin_provider:
                configured && key !== "k1" ? "native" : undefined,
              api_key_id: key,
              key_position: key === "k1" ? 1 : 2,
              model: "old",
              upstream_model: "old",
              position: 2,
            },
            ...(configured
              ? []
              : [
                  {
                    provider,
                    api_key_id: key,
                    key_position: key === "k1" ? 1 : 2,
                    model: "alias",
                    upstream_model: "old",
                    position: 1,
                  },
                ]),
          ];
        }),
        unavailable_keys: [],
      });
    if (url.pathname.endsWith("/channel-options")) {
      const key = url.searchParams.get("api_key_id")!,
        provider = configured
          ? key === "k1"
            ? "native"
            : "copy"
          : `site-${key}`;
      return Response.json({
        revision: revisions[source],
        manageable: true,
        batch_revisions: true,
        channels: [
          { provider: "peer", model: "old" },
          { provider, model: "old", upstream_model: "old" },
          ...(configured
            ? []
            : [{ provider, model: "alias", upstream_model: "old" }]),
        ],
      });
    }
    throw new Error("Unexpected " + url.pathname);
  });
  vi.stubGlobal("fetch", fetch);
  return { writes, revisions, fetch };
}
it("discovers all site keys across sources but excludes other account groups; chains exact returned revisions", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  expect(
    f.fetch.mock.calls.filter(([url]) => url.includes("channel-options")),
  ).toHaveLength(0);
  expect(
    f.fetch.mock.calls.filter(([url]) => url.includes("channel-routes")),
  ).toHaveLength(2);
  expect(plan.targets.map((t) => [t.source, t.key])).toEqual([
    ["a", "k1"],
    ["a", "k2"],
    ["b", "k1"],
  ]);
  expect(f.writes).toHaveLength(0);
  const events: BatchProgress[] = [];
  await applyChannelBatch(plan, (p) => events.push(p));
  expect(f.writes.map((w) => w.body.revision)).toEqual(["r1", "r2", "r1"]);
  expect(
    f.writes.every((w) => w.method === "PATCH" && w.body.action === "replace"),
  ).toBe(true);
  expect(f.writes[0].body).toMatchObject({
    models: [],
    model_mappings: { new: "new", renamed: "old" },
    positions: { new: 1, renamed: 1 },
  });
  expect(events.filter((p) => p.state === "done")).toHaveLength(3);
});
it("discovers native and key-owned copies on every matching source, preserving provider identity", async () => {
  const f = fixture(true);
  const plan = await prepareChannelBatch(
    draft("all", true),
    new AbortController().signal,
  );
  expect(plan.targets.map((t) => [t.source, t.key, t.provider])).toEqual([
    ["a", "k1", "native"],
    ["a", "k2", "copy"],
    ["b", "k1", "native"],
    ["b", "k2", "copy"],
  ]);
  await applyChannelBatch(plan, () => {});
  expect(f.writes[1].body).toMatchObject({
    provider: "native",
    edit_provider: "copy",
    api_key_id: "k2",
    revision: "r2",
  });
});
it("rejects an incomplete inventory or a stale editor before any write", async () => {
  let f = fixture(false, true);
  await expect(
    prepareChannelBatch(draft(), new AbortController().signal),
  ).rejects.toThrow("全部来源");
  expect(f.writes).toHaveLength(0);
  f = fixture();
  f.revisions.a = "r9";
  await expect(
    prepareChannelBatch(draft(), new AbortController().signal),
  ).rejects.toThrow("编辑版本");
  expect(f.writes).toHaveLength(0);
});
it("refuses mixed-version backends before making any batch mutation", async () => {
  const f = fixture(),
    original = f.fetch.getMockImplementation()!;
  f.fetch.mockImplementation(async (input, init) => {
    const response = await original(input, init);
    if (input.includes("channel-routes")) {
      const data = await response.json();
      delete data.batch_revisions;
      return Response.json(data);
    }
    return response;
  });
  await expect(
    prepareChannelBatch(draft(), new AbortController().signal),
  ).rejects.toThrow("后端正在更新");
  expect(f.writes).toHaveLength(0);
});
it("stops on concurrent edits and does not silently adopt a newer revision", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  const events: BatchProgress[] = [];
  await applyChannelBatch(plan, (p) => {
    events.push(p);
    if (p.state === "done") f.revisions.a = "r99";
  });
  expect(f.writes).toHaveLength(1);
  expect(events.at(-1)).toMatchObject({
    state: "error",
    message: expect.stringContaining("配置已变化"),
  });
});
it("stops after an ambiguous write error and records partial progress without retrying", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  const original = f.fetch.getMockImplementation()!;
  f.fetch.mockImplementation(async (input, init) => {
    if (init?.method === "PATCH" && f.writes.length === 1)
      throw new Error("network lost");
    return original(input, init);
  });
  const events: BatchProgress[] = [];
  await applyChannelBatch(plan, (p) => events.push(p));
  expect(f.writes).toHaveLength(1);
  expect(events.filter((p) => p.state === "done")).toHaveLength(1);
  expect(events.at(-1)?.state).toBe("error");
});
it("position-only applies through the route endpoint without sending model or rename changes", async () => {
  const f = fixture(true);
  const plan = await prepareChannelBatch(
    { ...draft("positions", true), positions: { old: 1 } },
    new AbortController().signal,
  );
  await applyChannelBatch(plan, () => {});
  expect(f.writes).toHaveLength(4);
  expect(f.writes[0]).toEqual({
    path: "/analytics/v1/sources/a/channel-routes",
    method: "PATCH",
    body: {
      api_key_id: "k1",
      revision: "r1",
      moves: [{ provider: "native", model: "old", position: 1 }],
    },
  });
});
it.each([false, true])(
  "deletes only existing scoped bindings, with no model payload (native=%s)",
  async (configured) => {
    const f = fixture(configured);
    const plan = await prepareChannelBatch(
      {
        ...draft("delete", configured),
        anchor: { source: "a", key: "", provider: "", revision: "" },
      },
      new AbortController().signal,
    );
    await applyChannelBatch(plan, () => {});
    expect(f.writes).toHaveLength(configured ? 4 : 3);
    for (const w of f.writes) {
      expect(w.body).not.toHaveProperty("models");
      expect(w.body).not.toHaveProperty("model_mappings");
      expect(w.body).not.toHaveProperty("positions");
      expect(w.method).toBe(configured ? "DELETE" : "PATCH");
    }
    if (configured) expect(f.writes[1].body.provider).toBe("copy");
    else expect(f.writes[0].body.action).toBe("delete");
  },
);
it("can stop between bindings without interrupting or replaying the completed write", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  let stopped = false;
  await applyChannelBatch(
    plan,
    (p) => {
      if (p.state === "done") stopped = true;
    },
    () => stopped,
  );
  expect(f.writes).toHaveLength(1);
});

it("projects final positions when multiple related channels share a key and model", () => {
  const d = draft();
  const targets = ["one", "two"].map((provider) => ({
    ...binding,
    id: provider,
    provider,
    current: { old: "old" },
    revision: "r1",
    models: { new: "new" },
    mappings: { new: "new" },
    positions: { new: 1 },
    clamped: false,
  }));
  const catalogs = new Map([
    [
      JSON.stringify(["a", "k1"]),
      [
        { provider: "one", model: "old" },
        { provider: "two", model: "old" },
      ],
    ],
  ]);
  projectBatchPositions(d, targets, catalogs);
  expect(targets[0]).toMatchObject({
    positions: { new: 1 },
    finalPositions: { new: 2 },
  });
  expect(targets[1]).toMatchObject({
    positions: { new: 1 },
    finalPositions: { new: 1 },
  });
});

it("keeps self mappings in original selection during section-only batch edits",()=>{
 const t={...binding,current:{old:'old'},installed:{...binding.installed!,models:['old'],model_mappings:{old:'old'}}};
 expect(batchTargetSettings({...draft('aliases'),aliases:{renamed:'old'}},t,options).models).toEqual({old:'old',renamed:'old'});
 expect(batchTargetSettings({...draft('models'),originals:{new:'new'}},t,options).models).toEqual({new:'new'});
});
it("explicit editor choices extend unconfigured models in every existing native binding",async()=>{
 const f=fixture(true);
 const plan=await prepareChannelBatch({...draft('all',true),allowUnverifiedModels:true,originals:{'extra-model':'extra-model'},aliases:{},models:{'extra-model':'extra-model'},positions:{'extra-model':1}},new AbortController().signal);
 await applyChannelBatch(plan,()=>{});
 expect(f.writes).toHaveLength(4);
 expect(f.writes.every(w=>w.body.allow_unverified_models===true)).toBe(true);
 expect(f.writes[0].body.model_mappings).toEqual({'extra-model':'extra-model'});
});
