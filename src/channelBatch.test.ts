import { afterEach, expect, it, vi } from "vitest";
import {
  applyChannelBatch,
  buildChannelBatch,
  batchTargetSettings,
  prepareChannelBatch,
  projectBatchPositions,
} from "./channelBatch";
import type {
  BatchBinding,
  BatchDraft,
  BatchPart,
  BatchProgress,
  BatchSnapshot,
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
  atomic_batch: true,
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
        atomic_batch: true,
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
        atomic_batch: true,
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
it("discovers all site keys across sources but excludes other account groups; commits each source once with its exact revision", async () => {
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
  expect(f.writes.map((w) => w.body.revision)).toEqual(["r1", "r1"]);
  expect(
    f.writes.every((w) => w.method === "POST" && w.body.part === "all"),
  ).toBe(true);
  expect(f.writes[0].body.targets[0]).toMatchObject({
    models: { new: "new", renamed: "old" },
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
  expect(f.writes[0].body.targets[1]).toMatchObject({
    origin_provider: "native",
    provider: "copy",
    api_key_id: "k2",
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
it("rejects a concurrently changed source without blocking independent sources", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  f.revisions.a = "r99";
  const events: BatchProgress[] = [];
  await applyChannelBatch(plan, (p) => events.push(p));
  expect(f.writes).toHaveLength(2);
  expect(events.filter((p) => p.state === "error")).toHaveLength(2);
  expect(events.filter((p) => p.state === "done")).toHaveLength(1);
  expect(f.writes.every((w) => w.body.revision === "r1")).toBe(true);
});
it("dispatches independent sources before either completes and never retries an ambiguous write", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  const original = f.fetch.getMockImplementation()!;
  const started: string[] = [];
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  f.fetch.mockImplementation(async (input, init) => {
    if (init?.method === "POST") {
      started.push(input);
      await pending;
      if (input.includes("/a/")) throw new Error("network lost");
    }
    return original(input, init);
  });
  const events: BatchProgress[] = [];
  const run = applyChannelBatch(plan, (p) => events.push(p));
  expect(started).toHaveLength(2);
  release();
  await run;
  expect(started).toHaveLength(2);
  expect(events.filter((p) => p.state === "error")).toHaveLength(2);
  expect(events.filter((p) => p.state === "done")).toHaveLength(1);
});
it("sends position-only changes together without changing model selection", async () => {
  const f = fixture(true);
  const plan = await prepareChannelBatch(
    { ...draft("positions", true), positions: { old: 1 } },
    new AbortController().signal,
  );
  await applyChannelBatch(plan, () => {});
  expect(f.writes).toHaveLength(2);
  expect(f.writes[0].body).toMatchObject({
    part: "positions",
    revision: "r1",
    targets: [
      {
        api_key_id: "k1",
        provider: "native",
        models: { old: "old" },
        positions: { old: 1 },
      },
      {
        api_key_id: "k2",
        provider: "copy",
        models: { old: "old" },
        positions: { old: 1 },
      },
    ],
  });
});
it.each([false, true])(
  "deletes only scoped bindings atomically without model payload (native=%s)",
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
    expect(f.writes).toHaveLength(2);
    for (const w of f.writes) {
      expect(w.body.part).toBe("delete");
      for (const t of w.body.targets) {
        expect(t).not.toHaveProperty("models");
        expect(t).not.toHaveProperty("positions");
      }
    }
  },
);
it("does not start requests after cancellation", async () => {
  const f = fixture();
  const plan = await prepareChannelBatch(draft(), new AbortController().signal);
  await applyChannelBatch(
    plan,
    () => {},
    () => true,
  );
  expect(f.writes).toHaveLength(0);
});

it("projects final positions when multiple related channels share a key and model", () => {
  const d = { ...draft(), positions: { new: 1 } };
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

it("keeps self mappings in original selection during section-only batch edits", () => {
  const t = {
    ...binding,
    current: { old: "old" },
    installed: {
      ...binding.installed!,
      models: ["old"],
      model_mappings: { old: "old" },
    },
  };
  expect(
    batchTargetSettings(
      { ...draft("aliases"), aliases: { renamed: "old" } },
      t,
      options,
    ).models,
  ).toEqual({ old: "old", renamed: "old" });
  expect(
    batchTargetSettings(
      { ...draft("models"), originals: { new: "new" } },
      t,
      options,
    ).models,
  ).toEqual({ new: "new" });
});
it("sends canonical extra models for server-side availability validation without an override", async () => {
  const f = fixture(true);
  const plan = await prepareChannelBatch(
    {
      ...draft("all", true),
      originals: { "extra-model": "extra-model" },
      aliases: {},
      models: { "extra-model": "extra-model" },
      positions: { "extra-model": 1 },
    },
    new AbortController().signal,
  );
  await applyChannelBatch(plan, () => {});
  expect(f.writes).toHaveLength(2);
  expect(f.writes.every((w) => !("allow_unverified_models" in w.body))).toBe(
    true,
  );
  expect(f.writes[0].body.targets[0].models).toEqual({
    "extra-model": "extra-model",
  });
});

it.each(["models", "all"] as const)(
  "%s batch retains the failed saved Luna pair only in its existing key and adds passing Sol elsewhere",
  async (part) => {
    const snapshot = resumeSnapshot(true);
    snapshot.routes.a.data.push({
      ...snapshot.routes.a.data[1],
      model: "gpt-6-luna",
      upstream_model: "gpt-6-luna",
      position: 1,
    });
    const models = {
      old: "old",
      "gpt-6-sol": "gpt-6-sol",
      "gpt-6-luna": "gpt-6-luna",
    };
    const d = {
      ...draft(part, true),
      originals: models,
      aliases: {},
      models,
      retainedOnly: { "gpt-6-luna": "gpt-6-luna" },
      positions: { old: 2, "gpt-6-sol": 1, "gpt-6-luna": 1 },
    };
    const plan = buildChannelBatch(d, snapshot, false);
    expect(plan.targets[0].models).toEqual(models);
    expect(plan.targets[0].omitted).toEqual([]);
    expect(plan.targets[1].models).toEqual({
      old: "old",
      "gpt-6-sol": "gpt-6-sol",
    });
    expect(plan.targets[1].omitted).toEqual(["gpt-6-luna"]);
    expect(plan.targets[1].finalPositions).not.toHaveProperty("gpt-6-luna");
    const writes: any[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, init?: RequestInit) => {
        writes.push(JSON.parse(String(init?.body)));
        return Response.json({ revision: "r3" });
      }),
    );
    await applyChannelBatch(plan, () => {});
    expect(writes).toHaveLength(2);
    expect(writes[0].targets[0].models).toHaveProperty("gpt-6-luna");
    expect(writes[1].targets[0].models).not.toHaveProperty("gpt-6-luna");
    expect(writes.every((w) => !w.allow_unverified_models)).toBe(true);
  },
);

it("alias batches retain a failed saved alias only where the exact upstream is already present", () => {
  const d = {
    ...draft("aliases"),
    aliases: { legacy: "failed", renamed: "new" },
    retainedOnly: { legacy: "failed" },
  };
  const saved = { ...binding, installed: {...binding.installed!, model_mappings:{legacy:"failed"}}, current: { old: "old", legacy: "failed" } };
  expect(batchTargetSettings(d, saved, options).models).toEqual({
    old: "old",
    legacy: "failed",
    renamed: "new",
  });
  const other = batchTargetSettings(d, binding, options);
  expect(other.models).toEqual({ old: "old", renamed: "new" });
  expect(other.omitted).toEqual(["legacy"]);
});

it("does not overwrite a different saved upstream with an unverified batch alias", () => {
  const d = {
    ...draft("aliases"),
    aliases: { alias: "failed" },
    retainedOnly: { alias: "failed" },
  };
  const next = batchTargetSettings(d, binding, options);
  expect(next.models).toEqual(binding.current);
  expect(next.omitted).toEqual(["alias"]);
});

it("route-only batches preserve every model regardless of the retention-only list", () => {
  const d = {
    ...draft("positions"),
    positions: { old: 1 },
    retainedOnly: { old: "old" },
  };
  expect(batchTargetSettings(d, binding, options).models).toEqual(
    binding.current,
  );
});

function resumeSnapshot(configured = false): BatchSnapshot {
  return {
    inventory: {
      data: configured ? [native("a"), native("b")] : [],
      unavailable_sources: [],
    },
    imports: {
      data: configured ? [] : [installed("a"), installed("b")],
      unavailable_sources: [],
      labels: {},
    },
    routes: Object.fromEntries(
      ["a", "b"].map((source) => [
        source,
        {
          revision: "r2",
          snapshot_consistent: true,
          manageable: true,
          batch_revisions: true,
          atomic_batch: true,
          unavailable_keys: [],
          data: [
            {
              api_key_id: "k1",
              key_position: 1,
              key_prefix: "masked",
              provider: "peer",
              model: "old",
              upstream_model: "old",
              position: 1,
            },
            ...Object.entries(
              configured ? { old: "old" } : { old: "old", alias: "old" },
            ).map(([model, upstream_model]) => ({
              api_key_id: "k1",
              key_position: 1,
              key_prefix: "masked",
              provider: configured ? "copy" : "site-k1",
              origin_provider: configured ? "native" : undefined,
              model,
              upstream_model,
              position: model === "old" ? 2 : 1,
            })),
          ],
        },
      ]),
    ),
  };
}
it("reconciles an interrupted model-selection batch by exact mappings, not model counts or local progress", async () => {
  const snapshot = resumeSnapshot();
  snapshot.imports.data[0].models = ["new", "alias"];
  snapshot.routes.a.data = snapshot.routes.a.data.map((row) =>
    row.provider === "site-k1" && row.model === "old"
      ? { ...row, model: "new", upstream_model: "new", position: 1 }
      : row,
  );
  const plan = buildChannelBatch(draft("models"), snapshot, false);
  expect(plan.targets.map((t) => !!t.skip)).toEqual([true, false]);
  expect(plan.targets[0].current).toEqual(plan.targets[0].models);
  // Both rows have two models, but only the actual matching one is skipped.
  expect(Object.keys(plan.targets[1].current)).toHaveLength(2);
  expect(Object.keys(plan.targets[1].models)).toHaveLength(2);
  const fetch = vi.fn(async (_input: string) =>
    Response.json({ revision: "r3" }),
  );
  vi.stubGlobal("fetch", fetch);
  await applyChannelBatch(plan, () => {});
  expect(fetch).toHaveBeenCalledOnce();
  expect(String(fetch.mock.calls[0]?.[0])).toContain(
    "/sources/b/channel-batch",
  );
});
it("resumes native edits after the original provider becomes a key-owned copy", () => {
  const snapshot = resumeSnapshot(true);
  const d = { ...draft("models", true), originals: { old: "old" } };
  const plan = buildChannelBatch(d, snapshot, false);
  expect(plan.targets).toHaveLength(2);
  expect(plan.targets.every((t) => t.skip?.includes("已一致"))).toBe(true);
});
it.each(["all", "positions"] as const)(
  "only skips matching routing positions for %s",
  (part) => {
    const snapshot = resumeSnapshot();
    const d = {
      ...draft(part),
      originals: { old: "old" },
      aliases: { alias: "old" },
      models: { old: "old", alias: "old" },
      positions: { old: 2, alias: 1 },
    };
    expect(
      buildChannelBatch(d, snapshot, false).targets.every((t) => t.skip),
    ).toBe(true);
    d.positions.old = 1;
    expect(
      buildChannelBatch(d, snapshot, false).targets.every((t) => !t.skip),
    ).toBe(true);
  },
);
it("does not conflate matching alias names with different upstream models", () => {
  const snapshot = resumeSnapshot();
  const d = { ...draft("aliases"), aliases: { alias: "new" } };
  expect(
    buildChannelBatch(d, snapshot, false).targets.every((t) => !t.skip),
  ).toBe(true);
});
it("does not reapply completed alias updates when source ordering changes", () => {
  const snapshot = resumeSnapshot();
  snapshot.routes.a.data.reverse(); // one provider per alias; model order is immaterial
  const d = { ...draft("aliases"), aliases: { alias: "old" } };
  expect(
    buildChannelBatch(d, snapshot, false).targets.every((t) => t.skip),
  ).toBe(true);
});
it("projects deterministic final order for multiple targets and is idempotent on repeat", () => {
  const snapshot = resumeSnapshot(true);
  snapshot.inventory.data.push({
    ...native("a"),
    provider: "z-other",
    name: "Other",
  });
  snapshot.imports.data = [
    {
      ...installed("a"),
      kind: "configured",
      provider: "native",
      account_id: "acct",
      group_id: 1,
    },
    {
      ...installed("a"),
      kind: "configured",
      provider: "z-other",
      account_id: "acct",
      group_id: 1,
    },
  ];
  snapshot.routes.a.data.push({
    ...snapshot.routes.a.data[1],
    provider: "z-other",
    origin_provider: undefined,
    position: 3,
  });
  const d = {
    ...draft("all"),
    originals: { old: "old" },
    aliases: {},
    models: { old: "old" },
    positions: { old: 1 },
    anchor: { source: "a", key: "k1", provider: "native", revision: "r1" },
  };
  // Verified group binding fixture includes both providers.
  snapshot.imports.data.forEach((c) => {
    c.binding_status = "matched";
    c.bound_keys = [{ account_id: "acct", group_id: 1 }] as typeof c.bound_keys;
  });
  const plan = buildChannelBatch(d, snapshot, false);
  const rows = snapshot.routes.a.data;
  snapshot.routes.a.data = [rows[2], rows[1], rows[0]].map((r, i) => ({
    ...r,
    position: i + 1,
  }));
  const repeated = buildChannelBatch(d, snapshot, false);
  expect(plan.targets.map((t) => t.finalPositions?.old)).toEqual([2, 1]);
  expect(repeated.targets.every((t) => t.skip)).toBe(true);
});
