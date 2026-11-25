import type { OptimizationSnapshot } from "./channelOptimizationPlan";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { SubModelCheck } from "./sub2apiResults";
import type { InstalledChannel } from "./sub2apiImports";
import { subFilterDefaults } from "./sub2apiPreferences";
export function optimizationFixture(): OptimizationSnapshot {
  const target: SubTarget = {
    group_id: 1,
    name: "Group",
    platform: "openai",
    channel: "p",
    rate: 0.1,
    billing: { rate: 0.1, source: "key", checked_at: 1 },
    key_id: 1,
    active: true,
    state: "done",
    message: "",
    result: null,
    models: [modelCheck("old"), modelCheck("new")],
  };
  const account: SubAccount = {
    id: "a",
    name: "Site",
    base: "https://site.test",
    email: "",
    state: "idle",
    message: "",
    synced_at: 1,
    targets: [target],
  };
  const installed: InstalledChannel = {
    account_id: "a",
    group_id: 1,
    source_id: "s",
    source_name: "Fugue",
    api_key_id: "key",
    key_position: 1,
    key_prefix: "masked",
    provider: "p",
    engine: "codex",
    engine_mode: "auto",
    name: "Site-0.1",
    models: ["old"],
    positions: { old: 1 },
    revision: "r1",
    manageable: true,
  };
  return {
    rows: [
      {
        id: "a:1",
        account,
        target,
        accountIds: ["a"],
        checks: target.models!,
        selected: undefined,
      },
    ],
    accounts: [account],
    inventory: [],
    imports: { data: [installed], labels: {}, unavailable_sources: [] },
    routes: {
      s: {
        data: [
          {
            provider: "p",
            model: "old",
            upstream_model: "old",
            api_key_id: "key",
            key_prefix: "masked",
            key_position: 1,
            position: 1,
          },
          {
            provider: "peer",
            model: "new",
            api_key_id: "key",
            key_prefix: "masked",
            key_position: 1,
            position: 1,
          },
        ],
        revision: "r1",
        snapshot_consistent: true,
        manageable: true,
        atomic_batch: true,
        optimize_batch: true,
        unavailable_keys: [],
      },
    },
    checks: [],
    prices: [],
    filters: { ...subFilterDefaults },
  };
}
export function existingProtocolFixture(provider = "p"): OptimizationSnapshot {
  const snapshot = optimizationFixture();
  const { imports, routes, filters, accounts } = snapshot;
  routes.s.optimize_extended = routes.s.legacy_engine_migration = true;
  filters.model = "gpt-6.2-sol";
  accounts[0].targets[0].models = [modelCheck("gpt-6.2-sol")];
  const original = imports.data[0];
  Object.assign(original, {
    provider: "p--messages",
    name: "kedaya-0.11",
    engine: "claude",
    models: ["old"],
  });
  Object.assign(routes.s.data[0], {
    provider: original.provider,
    engine: "claude",
    engine_mode: "auto",
  });
  routes.s.data[1].model = routes.s.data[1].upstream_model = filters.model;
  imports.data.push({
    ...original,
    provider,
    name: "kedaya Responses",
    engine: "codex",
    models: ["kept-alias"],
    model_mappings: { "kept-alias": "kept-upstream" },
    positions: { "kept-alias": 2 },
  });
  routes.s.data.push(
    { ...routes.s.data[0], provider: "peer", model: "kept-alias" },
    {
      ...routes.s.data[0],
      provider,
      engine: "codex",
      model: "kept-alias",
      upstream_model: "kept-upstream",
      position: 2,
    },
  );
  return snapshot;
}
export function fingerprintOptimizationFixture() {
  const snapshot = optimizationFixture();
  snapshot.fingerprints = [
    {
      account_id: "a",
      group_id: 1,
      model: "old",
      revision: "site-rev",
      state: "done",
      message: "",
      result: {
        model: "old",
        status: "mismatch",
        probability: 0,
        decision: "scored",
        prediction: "other",
        prediction_name: "其他模型",
        expected_class: "old",
        checked_at: 100,
        reference_commit: "ref",
        used_outputs: 3,
        candidates: [
          { model: "other", display_name: "其他模型", probability: 0.94 },
          { model: "old", display_name: "请求模型", probability: 0 },
          { model: "unknown-prob", display_name: "", probability: null },
        ],
      },
    },
  ];
  return snapshot;
}
export function protocolSwitchFixture(
  destination?: "new" | "existing" | "already-serving",
) {
  const s = existingProtocolFixture();
  s.routes.s.optimize_protocol_moves = true;
  const model = "gpt-6.2-sol";
  const [from, to] = s.imports.data;
  from.models.push(model, "sol-alias");
  from.model_mappings = { "sol-alias": model };
  const [originalRoute, peer] = s.routes.s.data;
  s.routes.s.data.splice(
    1,
    0,
    { ...originalRoute, model, upstream_model: model, position: 1 },
    {
      ...originalRoute,
      model: "sol-alias",
      upstream_model: model,
      position: 1,
    },
  );
  peer.position = 2;
  if (destination === "new") {
    s.imports.data.pop();
    s.routes.s.data = s.routes.s.data.filter((r) => r.provider !== to.provider);
  } else if (destination === "already-serving") {
    to.models.push(model);
    s.routes.s.data.push({
      ...peer,
      provider: to.provider,
      engine: "codex",
      position: 3,
    });
  }
  return s;
}
export function modelCheck(
  model: string,
  status: "success" | "error" = "success",
): SubModelCheck {
  return {
    model,
    state: "done",
    message: "",
    result: {
      model,
      checked_at: 10,
      verdict: "pass",
      availability: {
        status,
        text: "",
        ttft_ms: 1,
        duration_ms: 1,
        model_match: "match",
        protocol: "responses",
      },
      quality: { status: "skipped", text: "", ttft_ms: null, duration_ms: 0 },
    },
  };
}
