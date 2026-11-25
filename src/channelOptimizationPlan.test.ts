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
  existingProtocolFixture,
  fingerprintOptimizationFixture,
  optimizationFixture,
  modelCheck,
  protocolSwitchFixture,
} from "./channelOptimization.test-data";

const events = optimizationEvents.map((e) => e.id);

it.each(["existing", "new", "already-serving"] as const)(
  "switches an existing model with a %s destination, preserving aliases and peer priority",
  (destination) => {
    const s = protocolSwitchFixture(destination);
    const model = "gpt-6.2-sol";
    const to = destination === "new" ? "p--responses" : "p";
    const plan = buildOptimizationPlan(s, events);
    expect(plan.changes.map((c) => [c.action, c.model])).toEqual([
      ["switch", model],
      ["switch", "sol-alias"],
    ]);
    expect(plan.changes.every((c) => !c.blockedReason)).toBe(true);
    const req = optimizationRequests([plan.changes[0]], s.routes).get("s")!;
    expect(req.part).toBe("optimize_protocol");
    expect(req.protocol_moves).toEqual([
      {
        api_key_id: "key",
        from_provider: "p--messages",
        to_provider: to,
        model,
        protocol: "responses",
      },
    ]);
    const from = req.targets.find((t) => t.provider === "p--messages")!;
    expect(from.current).toEqual({
      old: "old",
      [model]: model,
      "sol-alias": model,
    });
    if (destination === "new") {
      expect(from).toMatchObject({
        models: { old: "old", [model]: model, "sol-alias": model },
        split_protocols: { [model]: "responses" },
        positions: { [model]: 1 },
      });
      expect(req.targets).toHaveLength(1);
    } else {
      expect(from.models).toEqual({ old: "old", "sol-alias": model });
      expect(req.targets.find((t) => t.provider === to)).toMatchObject({
        models: { "kept-alias": "kept-upstream", [model]: model },
        positions: {
          "kept-alias": 2,
          [model]: destination === "already-serving" ? 2 : 1,
        },
      });
    }
  },
);

it("never proposes a switch based on unfinished or unhealthy evidence or an old backend", () => {
  const s = protocolSwitchFixture();
  const check = s.accounts[0].targets[0].models![0];
  check.state = "running";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  check.state = "done";
  check.result!.availability.model_match = "mismatch";
  expect(buildOptimizationPlan(s, ["protocol-switch"]).changes).toEqual([]);
  check.result!.availability.model_match = "match";
  check.result!.availability.terminal_status = "missing";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  check.result!.availability.terminal_status = "complete";
  expect(
    buildOptimizationPlan(s, events).changes.some((c) => c.action === "switch"),
  ).toBe(true);
  expect(
    buildOptimizationPlan(s, ["add", "protocol"]).changes.some(
      (c) => c.action === "switch",
    ),
  ).toBe(false);
  s.routes.s.optimize_protocol_moves = false;
  expect(
    buildOptimizationPlan(s, events).changes.some((c) => c.action === "switch"),
  ).toBe(false);
});

it("leaves a manual source protocol and a conflicting destination alias unchanged", () => {
  const s = protocolSwitchFixture();
  s.routes.s.data[0].engine_origin = "user_override";
  for (const r of s.routes.s.data.filter((r) => r.provider === "p--messages"))
    r.engine_origin = "user_override";
  expect(
    buildOptimizationPlan(s, ["protocol-switch"]).changes[0].blockedReason,
  ).toContain("人工覆盖");
  for (const r of s.routes.s.data) delete r.engine_origin;
  const alias = "sol-alias";
  const target = s.imports.data[1];
  target.models.push(alias);
  target.model_mappings![alias] = "different";
  s.routes.s.data.push({
    ...s.routes.s.data.at(-1)!,
    model: alias,
    upstream_model: "different",
  });
  const plan = buildOptimizationPlan(s, events);
  expect(plan.changes.find((c) => c.model === alias)?.blockedReason).toContain(
    "不能覆盖现有映射",
  );
  expect(plan.changes.find((c) => c.model === "gpt-6.2-sol")?.action).toBe(
    "switch",
  );
});

it("removes a confirmed fingerprint mismatch independently of the response model name", () => {
  const snapshot = fingerprintOptimizationFixture();
  const old = snapshot.accounts[0].targets[0].models![0];
  expect(old.result?.availability.model_match).toBe("match");
  const [record] = snapshot.fingerprints!;
  const verdict = record.result!;
  expect(buildOptimizationPlan(snapshot, ["match"]).changes).toEqual([]);
  expect(
    buildOptimizationPlan(snapshot, ["fingerprint"]).changes,
  ).toMatchObject([
    {
      model: "old",
      action: "remove",
      reasons: ["模型指纹不匹配"],
      fingerprintDetails: [{ probability: 0, candidates: verdict.candidates }],
    },
  ]);
  snapshot.fingerprints = [
    { ...record, state: "running", previous_result: verdict },
  ];
  expect(buildOptimizationPlan(snapshot, ["fingerprint"]).changes).toEqual([]);
  snapshot.fingerprints = [
    { ...record, result: { ...verdict, used_outputs: 2 } },
  ];
  expect(buildOptimizationPlan(snapshot, ["fingerprint"]).changes).toEqual([]);
  snapshot.fingerprints = [{ ...record, group_id: 2 }];
  expect(buildOptimizationPlan(snapshot, ["fingerprint"]).changes).toEqual([]);
  snapshot.fingerprints = [record];
  snapshot.fingerprintReference = "new-reference";
  expect(buildOptimizationPlan(snapshot, ["fingerprint"]).changes).toEqual([]);
});
it("respects the billing discrepancy filter when proposing model additions", () => {
  const snapshot = optimizationFixture();
  const target = snapshot.accounts[0].targets[0];
  target.billing = { rate: 0.3, source: "key", checked_at: 1 };
  target.observed_rates = { new: { rate: 3, observed_at: 100 } };
  snapshot.filters.maxRate = "mismatch";
  expect(
    buildOptimizationPlan(snapshot, ["add"]).changes.map(
      (change) => change.model,
    ),
  ).toEqual(["new"]);
  target.observed_rates.new.rate = 0.3;
  expect(buildOptimizationPlan(snapshot, ["add"]).changes).toEqual([]);
  target.observed_rates = {};
  expect(buildOptimizationPlan(snapshot, ["add"]).changes).toEqual([]);
});

it("migrates only proven legacy defaults and can combine upgrade with a compatible addition", () => {
  const s = optimizationFixture();
  s.imports.data[0].engine = "gpt";
  s.routes.s.data[0].engine = "gpt";
  s.routes.s.data[0].engine_origin = "legacy_default";
  s.routes.s.legacy_engine_migration = true;
  const plan = buildOptimizationPlan(s, events);
  const migration = plan.changes.find((c) => c.action === "migrate")!;
  const added = plan.changes.find((c) => c.action === "add")!;
  expect(migration).toBeDefined();
  expect(added.blockedReason).toBeUndefined();
  expect(added.requiresLegacyMigration).toBe(true);
  expect(() => optimizationRequests([added], s.routes)).toThrow("同时勾选");
  expect(
    optimizationRequests(plan.changes, s.routes).get("s")!.targets[0],
  ).toMatchObject({
    migrate_legacy_engine: true,
    models: { old: "old", new: "new" },
  });
  expect(
    optimizationRequests([migration], s.routes).get("s")!.targets[0],
  ).toMatchObject({ migrate_legacy_engine: true, models: { old: "old" } });
  expect(buildOptimizationPlan(s, ["add"]).changes[0].blockedReason).toContain(
    "协议冲突",
  );
  for (const origin of ["unknown", "user_override"] as const) {
    s.routes.s.data[0].engine_origin = origin;
    expect(
      buildOptimizationPlan(s, events).changes.some(
        (c) => c.action === "migrate",
      ),
    ).toBe(false);
  }
  s.routes.s.data[0].engine_origin = "legacy_default";
  s.routes.s.legacy_engine_migration = false;
  expect(
    buildOptimizationPlan(s, events).changes.some(
      (c) => c.action === "migrate",
    ),
  ).toBe(false);
});

it("blocks protocol conflicts per binding before batch selection and preserves compatible changes", () => {
  const s = optimizationFixture();
  s.imports.data.push({
    ...s.imports.data[0],
    provider: "messages-copy",
    name: "Messages Site",
    engine: "claude",
  });
  s.routes.s.data.push({
    ...s.routes.s.data[0],
    provider: "messages-copy",
    engine: "claude",
    engine_mode: "auto",
  });
  const plan = buildOptimizationPlan(s, ["add"]);
  expect(plan.changes).toHaveLength(2);
  const blocked = plan.changes.find(
    (c) => c.binding.provider === "messages-copy",
  )!;
  expect(blocked.binding.protocol).toBe("messages");
  expect(blocked.detectedProtocols).toEqual(["responses"]);
  expect(blocked.blockedReason).toContain("Claude Messages");
  expect(blocked.blockedReason).toContain("Responses / Codex");
  expect(() => optimizationRequests(plan.changes, s.routes)).toThrow(
    "Messages Site",
  );
  expect(
    optimizationRequests(
      plan.changes.filter((c) => !c.blockedReason),
      s.routes,
    )
      .get("s")!
      .targets.map((t) => t.provider),
  ).toEqual(["p"]);
});

it("uses the effective caller-key engine instead of provider suffix or stale import metadata", () => {
  const s = optimizationFixture();
  s.imports.data[0].provider = "p--responses";
  s.routes.s.data[0].provider = "p--responses";
  s.routes.s.data[0].engine = "gpt";
  const change = buildOptimizationPlan(s, ["add"]).changes[0];
  expect(change.binding.engine).toBe("gpt");
  expect(change.blockedReason).toContain("Chat Completions");
  s.accounts[0].targets[0].models = [modelCheck("old", "error")];
  expect(
    buildOptimizationPlan(s, ["availability"]).changes[0].blockedReason,
  ).toBeUndefined();
});

it.each(["p", "p--responses"])(
  "offers one selectable addition on the original card when reusing %s",
  (provider) => {
    const s = existingProtocolFixture(provider);
    for (const reverse of [false, true]) {
      if (reverse) {
        // Swap the source/target discovery order without changing route priority.
        const list = s.routes.s.data;
        list.push(list.shift()!);
      }
      const plan = buildOptimizationPlan(s, events);
      expect(plan.changes).toHaveLength(1);
      expect(plan.changes[0]).toMatchObject({
        binding: { provider: "p--messages" },
        model: "gpt-6.2-sol",
        reuseBinding: { provider },
      });
      expect(plan.changes[0].blockedReason).toBeUndefined();
      expect(optimizationRequests(plan.changes, s.routes).get("s")).toEqual({
        revision: "r1",
        part: "optimize",
        targets: [
          {
            provider,
            account_id: "a",
            group_id: 1,
            api_key_id: "key",
            current: { "kept-alias": "kept-upstream" },
            models: {
              "kept-alias": "kept-upstream",
              "gpt-6.2-sol": "gpt-6.2-sol",
            },
            positions: { "kept-alias": 2, "gpt-6.2-sol": 2 },
          },
        ],
      });
    }
    const disabled = buildOptimizationPlan(s, ["add"]).changes;
    expect(
      disabled.find((c) => c.binding.provider === "p--messages")?.blockedReason,
    ).toContain("协议冲突");
    expect(
      disabled.find((c) => c.binding.provider === provider)?.blockedReason,
    ).toBeUndefined();
  },
);

it("does not re-add a model already served or overwrite a same-name alias on the target", () => {
  const s = existingProtocolFixture();
  const target = s.imports.data[1];
  const row = s.routes.s.data.at(-1)!;
  const model = "gpt-6.2-sol";
  target.models = [model];
  row.model = model;
  row.upstream_model = model;
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  target.model_mappings = { [model]: "other-upstream" };
  row.upstream_model = "other-upstream";
  const [change] = buildOptimizationPlan(s, events).changes;
  expect(change.blockedReason).toContain("不能覆盖现有映射");
  expect(() => optimizationRequests([change], s.routes)).toThrow(
    "不能覆盖现有映射",
  );
});

it("does not reuse a catalog row with an unconfirmed binding or the wrong group, key, or protocol", () => {
  const model = "gpt-6.2-sol";
  for (const mutate of [
    (s: OptimizationSnapshot) => {
      s.imports.data[1].group_id = 2;
    },
    (s: OptimizationSnapshot) => {
      s.imports.data[1].account_id = "other-account";
    },
    (s: OptimizationSnapshot) => {
      s.imports.data[1].models.push("missing-route");
    },
    (s: OptimizationSnapshot) => {
      s.routes.s.data.at(-1)!.engine = "claude";
    },
    (s: OptimizationSnapshot) => {
      s.imports.data[1].api_key_id = "other-key";
      s.routes.s.data.at(-1)!.api_key_id = "other-key";
    },
  ]) {
    const s = existingProtocolFixture("p--responses");
    mutate(s);
    const change = buildOptimizationPlan(s, events).changes.find(
      (c) => c.model === model && c.binding.provider === "p--messages",
    )!;
    expect(change.reuseBinding).toBeUndefined();
    expect(change.splitProtocol).toBeUndefined();
    expect(change.blockedReason).toContain("无法确认");
  }
});

it("merges a reused addition with a removal on the target without modifying the original", () => {
  const s = existingProtocolFixture();
  s.filters.model = "";
  s.accounts[0].targets[0].models!.push(modelCheck("kept-upstream", "error"));
  const plan = buildOptimizationPlan(s, events);
  expect(
    optimizationRequests(plan.changes, s.routes).get("s")?.targets,
  ).toMatchObject([
    {
      provider: "p",
      current: { "kept-alias": "kept-upstream" },
      models: { "gpt-6.2-sol": "gpt-6.2-sol" },
      positions: { "gpt-6.2-sol": 2 },
    },
  ]);
});

it("keeps unknown protocol additions visible but unselectable", () => {
  const s = optimizationFixture();
  delete s.imports.data[0].engine;
  expect(buildOptimizationPlan(s, ["add"]).changes[0].blockedReason).toContain(
    "当前渠道协议未确认",
  );
  s.imports.data[0].engine = "codex";
  delete s.accounts[0].targets[0].models![1].result!.availability.protocol;
  expect(buildOptimizationPlan(s, ["add"]).changes[0].blockedReason).toContain(
    "检测未记录",
  );
});
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
  setCurrent(s, ["gpt-old"]);
  t.models = [
    { ...modelCheck("gpt-old", "error"), state: "running" },
    { ...modelCheck("gpt-new"), state: "running" },
    { model: "unknown", state: "idle", message: "", result: null },
  ];
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.models = [modelCheck("gpt-old"), modelCheck("gpt-new")];
  t.tool_use = {
    status: "error",
    checked_at: 20,
    attempts: [],
    models: [
      {
        model: "gpt-old",
        state: "done",
        result: { status: "error", checked_at: 20, attempts: [] },
      },
      {
        model: "gpt-new",
        state: "done",
        result: { status: "error", checked_at: 20, attempts: [] },
      },
    ],
  };
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.tool_use.models![0].result!.status = "unsupported";
  expect(
    buildOptimizationPlan(s, events).changes.map((c) => [c.model, c.action]),
  ).toEqual([["gpt-old", "remove"]]);
  t.tool_use_state = "running";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
});
it("limits quality and compaction removals to the actually tested models and combines model filters with AND", () => {
  const s = optimizationFixture(),
    t = s.accounts[0].targets[0];
  t.models = [
    modelCheck("gpt-6-astra"),
    modelCheck("gpt-old"),
    modelCheck("gpt-new"),
  ];
  setCurrent(s, ["gpt-6-astra", "gpt-old", "gpt-new"]);
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
    model: "gpt-old",
    status: "unsupported",
    checked_at: 20,
    attempts: [],
  };
  expect(
    buildOptimizationPlan(s, events)
      .changes.map((c) => c.model)
      .sort(),
  ).toEqual(["gpt-6-astra", "gpt-old"]);
  s.filters.model = "gpt-new";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  s.filters.model = "";
  s.filters.modelMatch = "mismatch";
  expect(buildOptimizationPlan(s, events).changes).toEqual([]);
  t.models[1].result!.availability.model_match = "mismatch";
  expect(buildOptimizationPlan(s, events).changes.map((c) => c.model)).toEqual([
    "gpt-old",
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

it.each(["failed", "missing", "running"])(
  "does not add a canonical model using a passing alias target when its own check is %s",
  (state) => {
    const s = optimizationFixture();
    const native: ManagedChannel = {
      ...s.imports.data[0],
      kind: "configured",
      provider: "native",
      name: "Native",
      account_ids: ["a"],
      engine: "openai",
      probe_fingerprint: "fp",
      models: ["old", "new"],
      model_mappings: { new: "old" },
      binding_status: "matched",
      bound_keys: [
        {
          account_id: "a",
          account_name: "Site",
          base: "https://site.test",
          group_id: 1,
          remote_key_id: 1,
        },
      ],
    };
    const ownCheck = modelCheck("new", "error");
    if (state === "running") ownCheck.state = "running";
    s.accounts[0].targets[0].models = [
      modelCheck("old"),
      ...(state === "missing" ? [] : [ownCheck]),
    ];
    s.inventory = [native];
    s.imports.data = [native];
    s.rows = [{ ...s.rows[0], configured: native }];
    s.routes.s.data = [{ ...s.routes.s.data[0], provider: "native" }];
    s.filters.model = "new";
    expect(buildOptimizationPlan(s, ["add"]).changes).toEqual([]);
    // A real probe for this canonical upstream now allows it, despite the
    // provider's unrelated public alias having the same name.
    s.accounts[0].targets[0].models = [modelCheck("old"), modelCheck("new")];
    const plan = buildOptimizationPlan(s, ["add"]);
    expect(plan.changes.map((c) => [c.model, c.upstream, c.action])).toEqual([
      ["new", "new", "add"],
    ]);
    expect(
      optimizationRequests(plan.changes, s.routes).get("s")!.targets[0].models,
    ).toEqual({ old: "old", new: "new" });
  },
);

it("does not remove Claude or Gemini models due to historical Codex capability failures", () => {
  const s = optimizationFixture(),
    t = s.accounts[0].targets[0];
  const models = ["claude-opus-4-8", "gemini-3.1-pro", "codex-auto-review"];
  t.models = models.map((model) => modelCheck(model));
  setCurrent(s, models);
  t.tool_use = {
    status: "unsupported",
    checked_at: 20,
    attempts: [],
    models: models.map((model) => ({
      model,
      state: "done",
      result: { status: "unsupported", checked_at: 20, attempts: [] },
    })),
  };
  t.compaction = {
    model: models[0],
    status: "unsupported",
    checked_at: 20,
    attempts: [],
  };
  expect(
    buildOptimizationPlan(s, ["tool", "compaction"]).changes.map(
      (c) => c.model,
    ),
  ).toEqual(["codex-auto-review"]);
});
