import { controlRequest } from "./api";
import { modelPositionLimit } from "./ModelPositions";
import { boundGroups } from "./sub2apiImports";
import type { InstalledChannel, SubImports } from "./sub2apiImports";
import type { ManagedChannel } from "./channelManagement";
import type { Routes } from "./channelRouteData";

export type BatchScope =
  | { kind: "site"; account: string; group: number }
  | { kind: "configured"; source: string; provider: string };
export type BatchPart = "all" | "models" | "aliases" | "positions" | "delete";
export const batchPartLabels = {
  all: "全部设置",
  models: "模型勾选",
  aliases: "模型重命名",
  positions: "路由位置",
  delete: "删除所有已保存接入",
};
export interface BatchDraft {
  scope: BatchScope;
  name: string;
  // Canonical upstream names: never reinterpret an alias through another source.
  models: Record<string, string>;
  originals: Record<string, string>;
  aliases: Record<string, string>;
  part: BatchPart;
  positions: Record<string, number>;
  anchor: { source: string; key: string; provider: string; revision: string };
}
interface Options {
  batch_revisions?: boolean;
  revision: string;
  manageable: boolean;
  channels: { provider: string; model: string; upstream_model?: string }[];
}
export interface BatchBinding {
  id: string;
  source: string;
  sourceName: string;
  key: string;
  keyPosition: number;
  name: string;
  provider: string;
  native?: ManagedChannel;
  installed?: InstalledChannel;
  current: Record<string, string>;
}
export interface BatchTarget extends BatchBinding {
  models: Record<string, string>;
  revision: string;
  mappings: Record<string, string>;
  positions: Record<string, number>;
  finalPositions?: Record<string, number>;
  clamped: boolean;
  skip?: string;
}
export interface BatchPlan {
  draft: BatchDraft;
  targets: BatchTarget[];
}
export interface BatchProgress {
  id: string;
  state: "running" | "done" | "error";
  message?: string;
}

const idOf = (source: string, key: string, provider: string) =>
  JSON.stringify([source, key, provider]);
const read = <T>(path: string, signal: AbortSignal) =>
  controlRequest<T>(path, {
    signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
  });
const optionsPath = (t: { source: string; key: string }) =>
  "/v1/sub2api/channel-options?" +
  new URLSearchParams({ source_id: t.source, api_key_id: t.key });

export function batchTargetSettings(
  draft: BatchDraft,
  t: BatchBinding,
  options: Options,
) {
  if (draft.part === "delete")
    return { models: {}, positions: {}, clamped: false };
  const currentOriginals: Record<string, string> = {},
    currentAliases: Record<string, string> = {};
  for (const [name, up] of Object.entries(t.current)) {
    const original = t.native
      ? t.native.models.includes(name) &&
        (t.native.model_mappings?.[name] || name) === up
      : !t.installed?.model_mappings?.[name];
    (original ? currentOriginals : currentAliases)[name] = up;
  }
  const originals =
    draft.part === "all" || draft.part === "models"
      ? draft.originals
      : currentOriginals;
  const aliases =
    draft.part === "all" || draft.part === "aliases"
      ? draft.aliases
      : currentAliases;
  const collision = Object.keys(aliases).find((m) => m in originals);
  if (collision)
    throw new Error(
      `${t.sourceName} · Key ${t.keyPosition} 的 ${collision} 与保留的模型名称冲突。请调整重命名，或使用全部设置应用。`,
    );
  const models = { ...originals, ...aliases };
  if (!Object.keys(models).length)
    throw new Error(
      `${t.sourceName} · Key ${t.keyPosition} 将没有任何模型，请保留至少一个模型。`,
    );
  const currentPositions: Record<string, number> = {},
    counts: Record<string, number> = {};
  const seen = new Set<string>();
  for (const c of options.channels) {
    const key = JSON.stringify([c.provider, c.model]);
    if (seen.has(key)) continue;
    seen.add(key);
    counts[c.model] = (counts[c.model] || 0) + 1;
    if (c.provider === t.provider) currentPositions[c.model] = counts[c.model];
  }
  const positions: Record<string, number> = {};
  let clamped = false;
  for (const m of Object.keys(models)) {
    const requested =
      draft.part === "all"
        ? draft.positions[m] || 1
        : draft.part === "positions"
          ? (draft.positions[m] ?? currentPositions[m] ?? 1)
          : (currentPositions[m] ?? 1);
    positions[m] = Math.min(
      requested,
      modelPositionLimit(m, options.channels, t.provider),
    );
    clamped ||= positions[m] !== requested;
  }
  return {
    models,
    positions,
    clamped,
    skip:
      draft.part === "positions" &&
      !Object.keys(models).some((m) => m in draft.positions)
        ? "无对应模型，保持原配置"
        : undefined,
  };
}

export interface BatchInventory {
  data: ManagedChannel[];
  unavailable_sources: string[];
}
export interface BatchSnapshot {
  inventory: BatchInventory;
  imports: SubImports;
  routes: Record<string, Routes>;
}
function batchMembers(
  scope: BatchScope,
  inventory: BatchInventory,
  imports: SubImports,
) {
  const missing = [
    ...new Set([
      ...inventory.unavailable_sources,
      ...imports.unavailable_sources,
    ]),
  ];
  if (missing.length)
    throw new Error(
      `无法读取全部来源（${missing.join("、")}），请恢复连接后重试。`,
    );
  let native: ManagedChannel[], installed: InstalledChannel[];
  if (scope.kind === "configured") {
    const anchor = inventory.data.find(
      (c) => c.source_id === scope.source && c.provider === scope.provider,
    );
    if (!anchor) throw new Error("原渠道已变化，请重新打开编辑。");
    native = inventory.data.filter(
      (c) => c.provider === anchor.provider && c.engine === anchor.engine,
    );
    installed = [];
  } else {
    installed = imports.data.filter(
      (c) =>
        c.kind !== "configured" &&
        c.account_id === scope.account &&
        c.group_id === scope.group,
    );
    // Only verified upstream key/group bindings can extend a site's scope.
    const matched = new Set(
      imports.data
        .filter(
          (c) =>
            c.kind === "configured" &&
            boundGroups(c).some(
              (g) =>
                g.account_id === scope.account && g.group_id === scope.group,
            ),
        )
        .map((c) => JSON.stringify([c.source_id, c.provider])),
    );
    native = inventory.data.filter((c) =>
      matched.has(JSON.stringify([c.source_id, c.provider])),
    );
  }
  return { native, installed };
}

// The page already loads these catalogs for its channel counts and selectors.
// Preview generation is synchronous and contains no network calls.
export function buildChannelBatch(
  draft: BatchDraft,
  snapshot: BatchSnapshot,
  strictAnchor = true,
): BatchPlan {
  if (draft.part !== "delete" && !draft.anchor.revision)
    throw new Error("请先读取当前配置。");
  if (draft.part === "all" && !Object.keys(draft.models).length)
    throw new Error("请至少保留一个模型。");
  if (draft.part === "positions" && !Object.keys(draft.positions).length)
    throw new Error("请先选择需要调整位置的模型。");
  const { native, installed } = batchMembers(
    draft.scope,
    snapshot.inventory,
    snapshot.imports,
  );
  const nativeBySource = new Map<string, Map<string, ManagedChannel>>();
  for (const member of native) {
    const providers =
      nativeBySource.get(member.source_id) || new Map<string, ManagedChannel>();
    providers.set(member.provider, member);
    nativeBySource.set(member.source_id, providers);
  }
  const catalogs = new Map<string, Options["channels"]>();
  // Index the full catalog once instead of scanning it for each target key.
  for (const [source, routes] of Object.entries(snapshot.routes)) {
    for (const row of routes.data) {
      const key = JSON.stringify([source, row.api_key_id]);
      const entries = catalogs.get(key) || [];
      entries.push(row);
      catalogs.set(key, entries);
    }
  }
  const bindings = new Map<string, BatchBinding>();
  for (const c of installed) {
    if (!c.manageable) throw new Error(`${c.source_name} 尚不支持编辑此渠道。`);
    const id = idOf(c.source_id, c.api_key_id, c.provider);
    bindings.set(id, {
      id,
      source: c.source_id,
      sourceName: c.source_name,
      key: c.api_key_id,
      keyPosition: c.key_position,
      name: c.name,
      provider: c.provider,
      installed: c,
      current: Object.fromEntries(
        c.models.map((m) => [m, c.model_mappings?.[m] || m]),
      ),
    });
  }
  for (const source of [...new Set(native.map((c) => c.source_id))]) {
    const routes = snapshot.routes[source];
    if (!routes) throw new Error("来源路由尚未加载完整，请重新读取。");
    if (routes.unavailable_keys.length)
      throw new Error(
        `${native.find((c) => c.source_id === source)?.source_name} 的部分 API key 路由读取失败。`,
      );
    for (const row of routes.data) {
      const member =
        nativeBySource.get(source)?.get(row.provider) ||
        nativeBySource.get(source)?.get(row.origin_provider || "");
      if (!member) continue;
      const id = idOf(source, row.api_key_id, row.provider);
      const existing = bindings.get(id);
      if (existing?.installed) continue;
      const binding = existing || {
        id,
        source,
        sourceName: member.source_name,
        key: row.api_key_id,
        keyPosition: row.key_position,
        name: member.name,
        provider: row.provider,
        native: member,
        current: {},
      };
      binding.current[row.model] = row.upstream_model || row.model;
      bindings.set(id, binding);
    }
  }
  const anchorId = idOf(
    draft.anchor.source,
    draft.anchor.key,
    draft.anchor.provider,
  );
  if (draft.part !== "delete" && !bindings.has(anchorId))
    throw new Error("正在编辑的接入已不存在，请刷新后重新编辑。");
  const revisions = new Map<string, string>();
  const usedCatalogs = new Map<string, Options["channels"]>();
  const targets: BatchTarget[] = [];
  for (const t of [...bindings.values()].sort(
    (a, b) =>
      a.source.localeCompare(b.source) ||
      a.keyPosition - b.keyPosition ||
      a.provider.localeCompare(b.provider),
  )) {
    const sourceRoutes = snapshot.routes[t.source];
    if (
      !sourceRoutes ||
      !sourceRoutes.snapshot_consistent ||
      !sourceRoutes.revision
    )
      throw new Error(
        `${t.sourceName} 的路由快照尚未就绪或已变化，请重新读取。`,
      );
    if (sourceRoutes.unavailable_keys.length)
      throw new Error(`${t.sourceName} 的部分 API key 路由读取失败。`);
    const catalogKey = JSON.stringify([t.source, t.key]);
    const keyCatalog = catalogs.get(catalogKey) || [];
    const options: Options = {
      revision: sourceRoutes.revision,
      batch_revisions: sourceRoutes.batch_revisions,
      manageable: !!sourceRoutes.manageable,
      channels: keyCatalog,
    };
    if (!options.batch_revisions)
      throw new Error(
        "后端正在更新批量操作能力，请稍后重新读取；尚未修改任何接入。",
      );
    usedCatalogs.set(catalogKey, options.channels);
    if (!options.revision || !options.manageable)
      throw new Error(`${t.sourceName} 尚不支持批量编辑所需的配置校验。`);
    const revision = revisions.get(t.source);
    if (revision && revision !== options.revision)
      throw new Error(`${t.sourceName} 的配置在读取期间发生变化，请重新预览。`);
    revisions.set(t.source, options.revision);
    if (
      strictAnchor &&
      draft.part !== "delete" &&
      t.source === draft.anchor.source &&
      options.revision !== draft.anchor.revision
    )
      throw new Error("当前编辑版本已变化，请重新读取配置后重试。");
    const current = options.channels.filter((c) => c.provider === t.provider);
    if (!current.length)
      throw new Error(
        `${t.sourceName} · Key ${t.keyPosition} 的渠道已变化，请重新预览。`,
      );
    // Reject a stale discovery snapshot (even if its source revision was read later).
    const actual = Object.fromEntries(
      current.map((c) => [c.model, c.upstream_model || c.model]),
    );
    if (
      Object.keys(actual).length !== Object.keys(t.current).length ||
      Object.entries(t.current).some(([name, up]) => actual[name] !== up)
    )
      throw new Error(
        `${t.sourceName} · Key ${t.keyPosition} 的模型已变化，请重新预览。`,
      );
    const settings = batchTargetSettings(draft, t, options);
    const mappings: Record<string, string> = {};
    for (const [name, upstream] of Object.entries(settings.models)) {
      if (!t.native) {
        mappings[name] = upstream;
        continue;
      }
      const member = t.native;
      const input =
        member.models.find(
          (m) =>
            m === upstream && (member.model_mappings?.[m] || m) === upstream,
        ) ||
        member.models.find(
          (m) => (member.model_mappings?.[m] || m) === upstream,
        );
      // The edit API also accepts real upstream names from this key's existing copy.
      if (!input && !Object.values(actual).includes(upstream))
        throw new Error(
          `${t.sourceName} 的 ${t.name} 未配置上游模型 ${upstream}，请调整模型后重试。`,
        );
      mappings[name] = input || upstream;
    }
    targets.push({ ...t, revision: options.revision, mappings, ...settings });
  }
  projectBatchPositions(draft, targets, usedCatalogs);
  return { draft, targets };
}

// Cold reads are one catalog per source, in bounded parallelism. Never one
// channel-options request per binding. Also used for fresh pre-submit validation.
export async function prepareChannelBatch(
  draft: BatchDraft,
  signal: AbortSignal,
  onRead?: (snapshot: BatchSnapshot) => void,
  strictAnchor = true,
): Promise<BatchPlan> {
  const [inventory, imports] = await Promise.all([
    read<BatchInventory>("/v1/channel-management", signal),
    read<SubImports>("/v1/sub2api/channels", signal),
  ]);
  const { native, installed } = batchMembers(draft.scope, inventory, imports);
  const sources = [
    ...new Set([
      ...native.map((c) => c.source_id),
      ...installed.map((c) => c.source_id),
    ]),
  ];
  const routes: Record<string, Routes> = {};
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(4, sources.length) }, async () => {
      while (next < sources.length) {
        const source = sources[next++];
        routes[source] = await read<Routes>(
          `/v1/sources/${encodeURIComponent(source)}/channel-routes`,
          signal,
        );
      }
    }),
  );
  const snapshot = { inventory, imports, routes };
  if (signal.aborted) throw signal.reason;
  const plan = buildChannelBatch(draft, snapshot, strictAnchor);
  onRead?.(snapshot);
  return plan;
}

// Reconfirm changes in membership, revision, or the previewed model/position
// outcome. Object property order alone must not change the comparison.
export function sameBatchPlan(a: BatchPlan, b: BatchPlan) {
  const record = (v: Record<string, unknown>) =>
    Object.entries(v).sort(([a], [b]) => a.localeCompare(b));
  const signature = (p: BatchPlan) =>
    p.targets
      .map((t) => ({
        id: t.id,
        revision: t.revision,
        current: record(t.current),
        models: record(t.models),
        mappings: record(t.mappings),
        positions: record(t.positions),
        finalPositions: record(t.finalPositions || {}),
        skip: t.skip,
      }))
      .sort((a, b) => a.id.localeCompare(b.id));
  return JSON.stringify(signature(a)) === JSON.stringify(signature(b));
}

// A site may have two related providers on the same caller key. Project their
// sequential edits together so the preview shows final positions, not two
// providers both claiming the same slot. The write positions remain explicit.
export function projectBatchPositions(
  draft: BatchDraft,
  targets: BatchTarget[],
  catalogs: Map<string, Options["channels"]>,
) {
  for (const [scope, catalog] of catalogs) {
    const group = targets.filter(
      (t) => JSON.stringify([t.source, t.key]) === scope,
    );
    const orders = new Map<string, string[]>();
    for (const c of catalog) {
      const row = orders.get(c.model) || [];
      if (!row.includes(c.provider)) row.push(c.provider);
      orders.set(c.model, row);
    }
    for (const t of group) {
      if (t.skip) continue;
      const affected =
        draft.part === "positions"
          ? Object.keys(t.models).filter((m) => m in draft.positions)
          : [...new Set([...Object.keys(t.current), ...Object.keys(t.models)])];
      for (const model of affected) {
        const order = (orders.get(model) || []).filter((p) => p !== t.provider);
        if (model in t.models) {
          const pos = Math.min(t.positions[model], order.length + 1);
          t.clamped ||= pos !== t.positions[model];
          t.positions[model] = pos;
          order.splice(pos - 1, 0, t.provider);
        }
        orders.set(model, order);
      }
    }
    for (const t of group)
      t.finalPositions = Object.fromEntries(
        Object.keys(t.models).map((model) => [
          model,
          (orders.get(model) || []).indexOf(t.provider) + 1,
        ]),
      );
  }
}

export async function applyChannelBatch(
  plan: BatchPlan,
  onProgress: (p: BatchProgress) => void,
  stopped: () => boolean = () => false,
) {
  const revisions = new Map(plan.targets.map((t) => [t.source, t.revision]));
  for (const t of plan.targets) {
    if (stopped()) return;
    if (t.skip) continue;
    onProgress({ id: t.id, state: "running" });
    try {
      const revision = revisions.get(t.source)!;
      // Never adopt a freshly read revision: only chain the exact revision returned
      // by our preceding successful write, so unrelated edits stop this batch.
      const options = await controlRequest<Options>(optionsPath(t));
      if (!options.batch_revisions)
        throw new Error("后端暂不支持批量版本校验，已停止后续操作。");
      if (options.revision !== revision)
        throw new Error("配置已变化，已停止后续应用。请核对后重新预览。");
      if (!options.channels.some((c) => c.provider === t.provider))
        throw new Error("渠道接入已变化，已停止后续应用。");
      if (
        Object.keys(t.positions).some(
          (m) =>
            t.positions[m] >
            modelPositionLimit(m, options.channels, t.provider),
        )
      )
        throw new Error("路由数量已变化，预览位置失效，请重新预览。");
      if (stopped()) return;
      const deleting = plan.draft.part === "delete";
      const positionsOnly = plan.draft.part === "positions";
      const result = await controlRequest<{ revision: string }>(
        positionsOnly
          ? `/v1/sources/${encodeURIComponent(t.source)}/channel-routes`
          : t.installed
            ? "/v1/sub2api/channels"
            : "/v1/channel-management",
        {
          method:
            positionsOnly || t.installed
              ? "PATCH"
              : deleting
                ? "DELETE"
                : "POST",
          signal: AbortSignal.timeout(60000),
          body: JSON.stringify(
            positionsOnly
              ? {
                  api_key_id: t.key,
                  revision,
                  moves: Object.keys(t.models)
                    .filter((m) => m in plan.draft.positions)
                    .map((model) => ({
                      provider: t.provider,
                      model,
                      position: t.positions[model],
                    })),
                }
              : {
                  source_id: t.source,
                  api_key_id: t.key,
                  revision,
                  ...(!deleting
                    ? {
                        models: [],
                        model_mappings: t.mappings,
                        position: 1,
                        positions: t.positions,
                      }
                    : {}),
                  ...(t.installed
                    ? {
                        action: deleting ? "delete" : "replace",
                        account_id: t.installed.account_id,
                        group_id: t.installed.group_id,
                      }
                    : deleting
                      ? { provider: t.provider }
                      : {
                          provider: t.native!.provider,
                          edit_provider: t.provider,
                        }),
                },
          ),
        },
      );
      if (!result.revision)
        throw new Error(
          "请求已返回，但未取得应用版本；请刷新核对，未继续提交其他接入。",
        );
      revisions.set(t.source, result.revision);
      onProgress({ id: t.id, state: "done" });
    } catch (e) {
      onProgress({
        id: t.id,
        state: "error",
        message: e instanceof Error ? e.message : "无法确认结果，请刷新核对",
      });
      return;
    }
  }
}
