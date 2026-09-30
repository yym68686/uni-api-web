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
      },
      quality: { status: "skipped", text: "", ttft_ms: null, duration_ms: 0 },
    },
  };
}
