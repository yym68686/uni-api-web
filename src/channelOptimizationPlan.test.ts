import { expect, it, vi } from "vitest";
import {
  applyOptimization,
  buildOptimizationPlan,
  managementJoinState,
  optimizationEvents,
  optimizationRequests,
} from "./channelOptimizationPlan";
import type { OptimizationSnapshot } from "./channelOptimizationPlan";
import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";

import {
  optimizationFixture,
  modelCheck,
} from "./channelOptimization.test-data";

const events = optimizationEvents.map((e) => e.id);
function setCurrent(s: OptimizationSnapshot, names: string[]) {
  s.imports.data[0].models = names;
  s.routes.s.data = names.map((model, i) => ({
    provider: "p",
    model,
    upstream_model: model,
    api_key_id: "key",
    key_prefix: "masked",
    key_position: 1,
    position: i + 1,
  }));
}

it("only adds available models to existing key bindings, appending them and preserving unrelated routes", () => {
  const s = optimizationFixture();
  const plan = buildOptimizationPlan(s, events);
  expect(plan.changes.map((c) => [c.model, c.action])).toEqual([
    ["new", "add"],
  ]);
  const request = optimizationRequests(plan.changes, s.routes).get("s")!;
  expect(request.targets[0]).toMatchObject({
    account_id: "a",
    models: { old: "old", new: "new" },
    positions: { old: 1, new: 2 },
  });
  expect(s.routes.s.data).toHaveLength(2);
  s.imports.data = [];
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
});
it("scopes by model and preserves unrelated models and aliases, including a last-model removal", () => {
  const s = optimizationFixture();
  s.accounts[0].targets[0].models = [
    modelCheck("old", "error"),
    modelCheck("new"),
  ];
  s.imports.data[0].models = ["alias", "new"];
  s.imports.data[0].model_mappings = { alias: "old" };
  s.routes.s.data = [
    { ...s.routes.s.data[0], model: "alias", upstream_model: "old" },
    { ...s.routes.s.data[0], model: "new", upstream_model: "new" },
  ];
  s.filters.model = "old";
  const plan = buildOptimizationPlan(s, events);
  expect(plan.changes.map((c) => [c.model, c.action])).toEqual([
    ["alias", "remove"],
  ]);
  expect(
    optimizationRequests(plan.changes, s.routes).get("s")!.targets[0].models,
  ).toEqual({ new: "new" });
  s.imports.data[0].models = ["alias"];
  s.routes.s.data = s.routes.s.data.slice(0, 1);
  expect(
    optimizationRequests(
      buildOptimizationPlan(s, events).changes,
      s.routes,
    ).get("s")!.targets[0].models,
  ).toEqual({});
});
it("does not treat unknown, running or tool errors as proof of unsupported capability", () => {
  const s = optimizationFixture(),
    t = s.accounts[0].targets[0];
  t.models = [
    { ...modelCheck("old", "error"), state: "running" },
    { ...modelCheck("new"), state: "running" },
    { model: "unknown", state: "idle", message: "", result: null },
  ];
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.models = [modelCheck("old"), modelCheck("new")];
  t.tool_use = {
    status: "error",
    checked_at: 20,
    attempts: [],
    models: [
      {
        model: "old",
        state: "done",
        result: { status: "error", checked_at: 20, attempts: [] },
      },
      {
        model: "new",
        state: "done",
        result: { status: "error", checked_at: 20, attempts: [] },
      },
    ],
  };
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.tool_use.models![0].result!.status = "unsupported";
  expect(
    buildOptimizationPlan(s, events).changes.map((c) => [c.model, c.action]),
  ).toEqual([["old", "remove"]]);
  t.tool_use_state = "running";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
});
it("limits quality and compaction removals to the actually tested models and combines model filters with AND", () => {
  const s = optimizationFixture(),
    t = s.accounts[0].targets[0];
  t.models = [modelCheck("gpt-6-astra"), modelCheck("old"), modelCheck("new")];
  setCurrent(s, ["gpt-6-astra", "old", "new"]);
  t.quality_check = {
    source_id: "s",
    provider: "p",
    model: "gpt-6-astra",
    verdict: "fail",
    checked_at: 20,
    text: "",
    duration_ms: 1,
  };
  t.compaction = {
    model: "old",
    status: "unsupported",
    checked_at: 20,
    attempts: [],
  };
  expect(
    buildOptimizationPlan(s, events)
      .changes.map((c) => c.model)
      .sort(),
  ).toEqual(["gpt-6-astra", "old"]);
  s.filters.model = "new";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  s.filters.model = "";
  s.filters.modelMatch = "mismatch";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.models[1].result!.availability.model_match = "mismatch";
  expect(buildOptimizationPlan(s, events).changes.map((c) => c.model)).toEqual([
    "old",
  ]);
});
it("blocks abnormal price additions and previews removals using verified unit prices", () => {
  const s = optimizationFixture(),
    t = s.accounts[0].targets[0];
  s.prices = [
    {
      model: "old",
      input: 1,
      output: 2,
      cache_read: 0,
      cache_write: 0,
      cache_write_1h: 0,
      verified: true,
    },
    {
      model: "new",
      input: 1,
      output: 2,
      cache_read: 0,
      cache_write: 0,
      cache_write_1h: 0,
      verified: true,
    },
  ];
  for (const c of t.models!)
    c.result!.availability.usage = {
      status: "matched",
      input_tokens: 100,
      output_tokens: 100,
      input_price: 10,
      output_price: 20,
    } as NonNullable<SubTarget["result"]>["availability"]["usage"];
  expect(
    buildOptimizationPlan(s, events).changes.map((c) => [
      c.model,
      c.action,
      c.reasons,
    ]),
  ).toEqual([["old", "remove", ["单价异常"]]]);
  expect(buildOptimizationPlan(s, ["add"]).changes).toEqual([]);
  s.filters.priceStatus = "normal";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
});
it("requires complete revisions and never treats unreadable routes as not joined", () => {
  const s = optimizationFixture();
  expect(managementJoinState(s.rows[0], s.imports, s.routes)).toBe("joined");
  s.routes.s.snapshot_consistent = false;
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  expect(buildOptimizationPlan(s, events).notes[0]).toContain("已跳过");
  s.imports.data = [];
  s.imports.unavailable_sources = ["s"];
  expect(managementJoinState(s.rows[0], s.imports, {})).toBe("unknown");
  s.imports.unavailable_sources = [];
  expect(managementJoinState(s.rows[0], s.imports, {})).toBe("unjoined");
});
it("keeps native evidence source/fingerprint scoped and rechecks source-specific filters", () => {
  const s = optimizationFixture();
  const native = {
    ...s.imports.data[0],
    kind: "configured",
    provider: "native",
    api_key_id: "",
    account_id: "",
    group_id: 0,
    account_ids: [],
    engine: "openai",
    probe_fingerprint: "fp",
  } as ManagedChannel;
  s.inventory = [native];
  s.imports.data = [native];
  s.rows = [{ ...s.rows[0], configured: native, accountIds: [] }];
  s.routes.s.data = [{ ...s.routes.s.data[0], provider: "native" }];
  const result = modelCheck("new").result!;
  s.checks = [
    {
      source_id: "other",
      provider: "native",
      kind: "model",
      model: "new",
      fingerprint: "fp",
      state: "done",
      message: "",
      result,
      history: undefined,
    },
  ];
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  s.checks[0].source_id = "s";
  s.checks[0].fingerprint = "old";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  s.checks[0].fingerprint = "fp";
  expect(buildOptimizationPlan(s, events).changes.map((c) => c.model)).toEqual([
    "new",
  ]);
  s.filters.maxRate = ".1";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  s.filters.maxRate = "";
  expect(managementJoinState(s.rows[0], s.imports, s.routes)).toBe("joined");
  s.routes.s.data = [];
  expect(managementJoinState(s.rows[0], s.imports, s.routes)).toBe("unjoined");
});

it("requires every bound group to allow an addition and deduplicates a channel visible through multiple rows", () => {
  const s = optimizationFixture();
  const native = {
    ...s.imports.data[0],
    kind: "configured",
    provider: "native",
    account_ids: ["a"],
    engine: "openai",
    probe_fingerprint: "fp",
    binding_status: "matched",
    bound_keys: [
      {
        account_id: "a",
        account_name: "Site",
        base: "https://site.test",
        group_id: 1,
        remote_key_id: 1,
      },
      {
        account_id: "a",
        account_name: "Site",
        base: "https://site.test",
        group_id: 2,
        remote_key_id: 2,
      },
    ],
  } as ManagedChannel;
  const target = {
    ...s.accounts[0].targets[0],
    group_id: 2,
    models: [modelCheck("old"), modelCheck("new")],
  };
  target.models[1].result!.availability.model_match = "mismatch";
  s.accounts[0].targets.push(target);
  s.inventory = [native];
  s.imports.data = [native];
  s.rows.push({ ...s.rows[0], configured: native, id: "native" });
  s.routes.s.data = [{ ...s.routes.s.data[0], provider: "native" }];
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  target.models[1].result!.availability.model_match = "match";
  expect(buildOptimizationPlan(s, events).changes.map((c) => c.model)).toEqual([
    "new",
  ]);
});

it("keeps existing routes in relative order when removing a peer and adding multiple channels", () => {
  const s = optimizationFixture();
  const base = buildOptimizationPlan(s, events).changes[0];
  const changes = [
    base,
    {
      ...base,
      id: "second",
      binding: { ...base.binding, id: "second", provider: "second" },
    },
    {
      ...base,
      id: "remove",
      action: "remove" as const,
      model: "new",
      binding: {
        ...base.binding,
        id: "remove",
        provider: "peer",
        current: { new: "new" },
      },
    },
  ];
  const request = optimizationRequests(changes, s.routes).get("s")!;
  expect(request.targets.map((t) => [t.provider, t.positions.new])).toEqual([
    ["p", 1],
    ["second", 2],
    ["peer", undefined],
  ]);
  expect(request.targets[0].models.old).toBe("old");
});
it("applies only selected changes once per source and reports revision conflicts without retries", async () => {
  const s = optimizationFixture();
  const changes = buildOptimizationPlan(s, events).changes;
  const fetch = vi.fn(
    async (_url: string, _init?: RequestInit) =>
      new Response("配置已变化，请重新核对", { status: 409 }),
  );
  vi.stubGlobal("fetch", fetch);
  const progress = vi.fn();
  await applyOptimization(changes, s.routes, progress);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(
    JSON.parse(fetch.mock.calls[0][1]!.body as string).targets,
  ).toHaveLength(1);
  expect(progress).toHaveBeenLastCalledWith(
    "s",
    "error",
    expect.stringContaining("配置已变化"),
  );
});
