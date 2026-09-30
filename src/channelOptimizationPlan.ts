import { controlRequest } from "./api";
import { boundGroups } from "./sub2apiImports";
import type { InstalledChannel, SubImports } from "./sub2apiImports";
import {
  channelMembers,
  configuredModelChecks,
  configuredQualityResult,
} from "./channelManagement";
import type {
  ConfiguredCheck,
  ManagedChannel,
  ManagementRow,
} from "./channelManagement";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { Routes } from "./channelRouteData";
import type { SubFilters } from "./sub2apiPreferences";
import {
  modelChecks,
  modelMatchStatus,
  groupQualityResult,
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { assessPrice, matchesPriceFilter } from "./sub2apiPriceCheck";
import { toolUseModels } from "./toolUse";
import type { ToolUseResult, ToolUseModel } from "./toolUse";
import type { CompactionResult } from "./SubCompaction";
import type { ModelPrice } from "./types";

export const optimizationEvents = [
  { id: "add", label: "新增可用模型" },
  { id: "availability", label: "取消检测失败的模型" },
  { id: "tool", label: "取消不支持 Tool use 的模型" },
  { id: "price", label: "取消单价异常的模型" },
  { id: "match", label: "取消模型不匹配的模型" },
  { id: "quality", label: "取消降智的 Astra 模型" },
  { id: "compaction", label: "取消不支持压缩的已检测模型" },
] as const;
export type OptimizationEvent = (typeof optimizationEvents)[number]["id"];
export interface OptimizationSnapshot {
  rows: ManagementRow[];
  accounts: SubAccount[];
  inventory: ManagedChannel[];
  imports: SubImports;
  routes: Record<string, Routes>;
  checks: ConfiguredCheck[];
  prices: ModelPrice[];
  filters: SubFilters;
}
export interface OptimizationBinding {
  id: string;
  source: string;
  sourceName: string;
  key: string;
  keyPosition: number;
  provider: string;
  name: string;
  installed?: InstalledChannel;
  native?: ManagedChannel;
  current: Record<string, string>;
  revision: string;
}
export interface OptimizationChange {
  id: string;
  binding: OptimizationBinding;
  model: string;
  upstream: string;
  action: "add" | "remove";
  reasons: string[];
  checkedAt: number;
}
export interface OptimizationPlan {
  changes: OptimizationChange[];
  notes: string[];
}
const providerID = (source: string, provider: string) =>
  JSON.stringify([source, provider]);
const bindingID = (source: string, key: string, provider: string) =>
  JSON.stringify([source, key, provider]);

// A configured provider only counts as joined when an actual caller key routes
// to it (or its key-owned copy). An unreadable source is never "not joined".
export function managementJoinState(
  row: ManagementRow,
  imports: SubImports | undefined,
  routes: Record<string, Routes>,
): "joined" | "unjoined" | "unknown" {
  if (!imports) return "unknown";
  const members = row.configured
    ? channelMembers(row.configured)
    : imports.data.filter((i) =>
        boundGroups(i).some(
          (g) =>
            g.account_id === row.account.id &&
            g.group_id === row.target.group_id,
        ),
      );
  if (
    members.some(
      (m) => m.kind !== "configured" && !!m.api_key_id && m.models.length > 0,
    )
  )
    return "joined";
  let unknown = !!imports.unavailable_sources?.length;
  for (const member of members) {
    const data = routes[member.source_id];
    if (
      data?.data.some(
        (r) =>
          r.api_key_id &&
          (r.provider === member.provider ||
            r.origin_provider === member.provider),
      )
    )
      return "joined";
    unknown ||=
      !data ||
      !!data.unavailable_keys?.length ||
      data.snapshot_consistent === false;
  }
  return unknown ? "unknown" : "unjoined";
}

interface Evidence {
  check: SubModelCheck;
  tool?: ToolUseModel;
  compaction?: CompactionResult;
  compactionState?: string;
  degraded: boolean;
  target?: SubTarget;
  accountId?: string;
  canAdd: boolean;
  qualityVerdict?: string;
}
const running = (state?: string) => state === "queued" || state === "running";
function siteTool(
  target: SubTarget | undefined,
  model: string,
): ToolUseModel | undefined {
  const saved = toolUseModels(target?.tool_use).find((c) => c.model === model);
  return running(target?.tool_use_state)
    ? { model, state: target!.tool_use_state!, result: saved?.result }
    : saved;
}
function evidenceFor(
  binding: OptimizationBinding,
  upstream: string,
  snapshot: OptimizationSnapshot,
): Evidence[] {
  const siteGroups = boundGroups(binding.installed || binding.native);
  const targets = siteGroups.map((group) =>
    snapshot.accounts
      .find((a) => a.id === group.account_id)
      ?.targets.find((t) => t.group_id === group.group_id),
  );
  if (!binding.native)
    return targets.map((target, i) => ({
      target,
      accountId: siteGroups[i]?.account_id,
      canAdd:
        !!target?.active &&
        !!target.key_id &&
        !running(
          snapshot.accounts.find((a) => a.id === siteGroups[i]?.account_id)
            ?.state,
        ),
      qualityVerdict: target ? groupQualityResult(target)?.verdict : undefined,
      check: target
        ? modelChecks(target).find((c) => c.model === upstream) || {
            model: upstream,
            state: "idle",
            message: "",
            result: null,
          }
        : { model: upstream, state: "idle", message: "", result: null },
      tool: siteTool(target, upstream),
      compaction: target?.compaction,
      compactionState: target?.compaction_state,
      degraded:
        upstream === "gpt-6-astra" &&
        !!target &&
        groupQualityResult(target)?.verdict === "fail",
    }));
  const member = binding.native;
  const native = snapshot.checks.filter(
    (c) =>
      c.source_id === member.source_id &&
      c.provider === member.provider &&
      (c.fingerprint || "") === (member.probe_fingerprint || ""),
  );
  const names = [
    ...new Set([
      upstream,
      ...member.models.filter(
        (m) => (member.model_mappings?.[m] || m) === upstream,
      ),
    ]),
  ];
  const toolCandidates = native
    .filter((c) => c.kind === "tool-use")
    .flatMap((c) =>
      c.model
        ? [
            {
              model: member.model_mappings?.[c.model] || c.model,
              state: c.state,
              result: c.result as ToolUseResult | null,
            },
          ]
        : toolUseModels(c.result as ToolUseResult | undefined).map((entry) => ({
            ...entry,
            model: member.model_mappings?.[entry.model] || entry.model,
            state: running(c.state) ? c.state : entry.state,
          })),
    )
    .filter((c) => c.model === upstream);
  const directTool = toolCandidates.sort(
    (a, b) =>
      Number(running(b.state)) - Number(running(a.state)) ||
      (b.result?.checked_at || 0) - (a.result?.checked_at || 0),
  )[0];
  const directCompaction = native
    .filter(
      (c) =>
        c.kind === "compaction" &&
        (c.result as CompactionResult | null)?.model === upstream,
    )
    .sort(
      (a, b) => (b.result?.checked_at || 0) - (a.result?.checked_at || 0),
    )[0];
  // Evaluate every bound key group, not a presentation row merged across sources.
  return (targets.length ? targets : [undefined]).map((target, i) => {
    const scoped: ManagedChannel = {
      ...member,
      kind: "configured",
      binding_status: "matched",
      bound_keys: siteGroups[i]
        ? [{ ...siteGroups[i], account_name: "", base: "", remote_key_id: 0 }]
        : [],
    };
    const checks = configuredModelChecks(
      scoped,
      snapshot.accounts,
      native,
      names,
    );
    const check = checks.find((c) => c.model === upstream) || checks[0];
    const savedTool = siteTool(target, upstream);
    const tool =
      directTool &&
      (running(directTool.state) ||
        (directTool.result?.checked_at || 0) >=
          (savedTool?.result?.checked_at || 0))
        ? directTool
        : savedTool;
    const compaction =
      directCompaction &&
      (directCompaction.result?.checked_at || 0) >=
        (target?.compaction?.checked_at || 0)
        ? (directCompaction.result as CompactionResult)
        : target?.compaction;
    return {
      target,
      accountId: siteGroups[i]?.account_id,
      canAdd:
        !target ||
        (!!target.active &&
          !running(
            snapshot.accounts.find((a) => a.id === siteGroups[i]?.account_id)
              ?.state,
          )),
      qualityVerdict: configuredQualityResult(scoped, snapshot.accounts, native)
        ?.verdict,
      check: { ...check, model: upstream },
      tool,
      compaction,
      compactionState:
        compaction === target?.compaction
          ? target?.compaction_state
          : directCompaction?.state,
      degraded:
        upstream === "gpt-6-astra" &&
        configuredQualityResult(scoped, snapshot.accounts, native)?.verdict ===
          "fail",
    };
  });
}

function modelMatchesFilters(
  e: Evidence,
  filters: SubFilters,
  prices: ModelPrice[],
) {
  const c = e.check;
  return (
    (!filters.accountId ||
      (filters.accountId === "__unassigned__"
        ? !e.accountId
        : filters.accountId === e.accountId)) &&
    (!filters.maxRate ||
      (e.target?.billing?.rate != null &&
        e.target.billing.rate <= Number(filters.maxRate))) &&
    (!filters.platform || e.target?.platform === filters.platform) &&
    (!filters.quality || e.qualityVerdict === filters.quality) &&
    (!filters.minQuality ||
      (!!e.target?.history?.successful &&
        e.target.history.passed * 100 >=
          Number(filters.minQuality) * e.target.history.successful)) &&
    (!filters.compaction ||
      (e.compaction?.status || "untested") === filters.compaction) &&
    (!filters.availability ||
      (filters.availability === "untested"
        ? !c.result
        : c.result?.availability.status === filters.availability)) &&
    matchesPriceFilter(filters.priceStatus, [c], prices) &&
    (!filters.modelMatch || modelMatchStatus(c) === filters.modelMatch) &&
    (!filters.toolUse ||
      (e.tool?.result?.status || "untested") === filters.toolUse)
  );
}
function faults(
  e: Evidence,
  model: string,
  prices: ModelPrice[],
): [OptimizationEvent, string][] {
  const result: [OptimizationEvent, string][] = [];
  if (!running(e.check.state)) {
    if (e.check.result?.availability.status === "error")
      result.push(["availability", "可用性检测失败"]);
    if (
      modelMatchStatus(e.check) === "mismatch" ||
      modelMatchStatus(e.check) === "invalid"
    )
      result.push(["match", "返回模型不匹配"]);
    if (assessPrice(e.check, prices).status === "abnormal")
      result.push(["price", "单价异常"]);
  }
  if (!running(e.tool?.state) && e.tool?.result?.status === "unsupported")
    result.push(["tool", "不支持 Tool use"]);
  if (e.degraded && !running(e.check.state))
    result.push(["quality", "Astra 最近检测降智"]);
  if (
    !running(e.compactionState) &&
    e.compaction?.model === model &&
    e.compaction.status === "unsupported"
  )
    result.push(["compaction", "该模型不支持远程压缩"]);
  return result;
}

export function buildOptimizationPlan(
  snapshot: OptimizationSnapshot,
  events: OptimizationEvent[],
): OptimizationPlan {
  const notes = new Set<string>(),
    changes: OptimizationChange[] = [];
  const allowedNative = new Map<string, ManagedChannel>();
  const allowedSite = new Map<string, InstalledChannel>();
  for (const row of snapshot.rows) {
    if (row.configured)
      for (const member of channelMembers(row.configured))
        allowedNative.set(
          providerID(member.source_id, member.provider),
          member,
        );
    else
      for (const installed of snapshot.imports.data) {
        if (
          !boundGroups(installed).some(
            (g) =>
              g.account_id === row.account.id &&
              g.group_id === row.target.group_id,
          )
        )
          continue;
        if (installed.kind === "configured") {
          const member = snapshot.inventory.find(
            (m) =>
              m.source_id === installed.source_id &&
              m.provider === installed.provider,
          );
          if (member)
            allowedNative.set(
              providerID(member.source_id, member.provider),
              member,
            );
        } else
          allowedSite.set(
            bindingID(
              installed.source_id,
              installed.api_key_id,
              installed.provider,
            ),
            installed,
          );
      }
  }
  const sources = new Set(
    [...allowedNative.values(), ...allowedSite.values()].map(
      (m) => m.source_id,
    ),
  );
  for (const source of sources) {
    const routes = snapshot.routes[source];
    if (
      !routes?.revision ||
      !routes.snapshot_consistent ||
      routes.unavailable_keys?.length ||
      !routes.manageable ||
      !routes.atomic_batch ||
      !routes.optimize_batch
    ) {
      notes.add(
        `${[...allowedNative.values(), ...allowedSite.values()].find((m) => m.source_id === source)?.source_name || source}：路由快照不完整或优化能力尚未就绪，已跳过。`,
      );
      continue;
    }
    const bindings = new Map<string, OptimizationBinding>();
    for (const route of routes.data) {
      const id = bindingID(source, route.api_key_id, route.provider);
      const installed = allowedSite.get(id);
      const native = allowedNative.get(
        providerID(source, route.origin_provider || route.provider),
      );
      if (!installed && !native) continue;
      const binding = bindings.get(id) || {
        id,
        source,
        sourceName: (installed || native)!.source_name,
        key: route.api_key_id,
        keyPosition: route.key_position,
        provider: route.provider,
        name: (installed || native)!.name,
        native: installed ? undefined : native,
        installed,
        current: {},
        revision: routes.revision,
      };
      binding.current[route.model] = route.upstream_model || route.model;
      bindings.set(id, binding);
    }
    for (const binding of bindings.values()) {
      if (
        binding.installed &&
        (Object.keys(binding.current).length !==
          binding.installed.models.length ||
          binding.installed.models.some(
            (m) =>
              binding.current[m] !==
              (binding.installed?.model_mappings?.[m] || m),
          ))
      ) {
        notes.add(
          `${binding.name} · Key ${binding.keyPosition}：模型配置已变化，已跳过，请刷新渠道管理。`,
        );
        continue;
      }
      const groups = boundGroups(binding.installed || binding.native);
      const names = new Set(Object.values(binding.current));
      for (const g of groups)
        for (const c of modelChecks(
          snapshot.accounts
            .find((a) => a.id === g.account_id)
            ?.targets.find((t) => t.group_id === g.group_id) ||
            ({ models: [], result: null } as unknown as SubTarget),
        ))
          names.add(c.model);
      if (binding.native)
        for (const c of snapshot.checks)
          if (
            c.source_id === source &&
            c.provider === binding.native.provider &&
            (c.kind === "model" || c.kind === "availability")
          )
            names.add(binding.native.model_mappings?.[c.model] || c.model);
      for (const upstream of names) {
        const publicModels = Object.keys(binding.current).filter(
          (m) => binding.current[m] === upstream,
        );
        const scope = snapshot.filters.model;
        if (
          scope &&
          scope !== upstream &&
          !publicModels.includes(scope) &&
          (binding.native?.model_mappings?.[scope] || scope) !== upstream
        )
          continue;
        const evidence = evidenceFor(binding, upstream, snapshot);
        if (
          !evidence.length ||
          !evidence.some((e) =>
            modelMatchesFilters(e, snapshot.filters, snapshot.prices),
          )
        )
          continue;
        const problems = evidence.flatMap((e) =>
          faults(e, upstream, snapshot.prices),
        );
        const reasons = [
          ...new Set(
            evidence
              .filter((e) =>
                modelMatchesFilters(e, snapshot.filters, snapshot.prices),
              )
              .flatMap((e) => faults(e, upstream, snapshot.prices))
              .filter(([event]) => events.includes(event))
              .map(([, reason]) => reason),
          ),
        ];
        const checkedAt = Math.max(
          0,
          ...evidence.map((e) => e.check.result?.checked_at || 0),
          ...evidence.map((e) => e.tool?.result?.checked_at || 0),
        );
        if (publicModels.length) {
          if (reasons.length)
            for (const model of publicModels) {
              if (scope && scope !== upstream && scope !== model) continue;
              changes.push({
                id: JSON.stringify([binding.id, model]),
                binding,
                model,
                upstream,
                action: "remove",
                reasons,
                checkedAt,
              });
            }
        } else if (
          events.includes("add") &&
          !Object.hasOwn(binding.current, upstream) &&
          !problems.length &&
          evidence.every(
            (e) =>
              modelMatchesFilters(e, snapshot.filters, snapshot.prices) &&
              e.canAdd &&
              e.check.state === "done" &&
              e.check.result?.availability.status === "success" &&
              !running(e.tool?.state) &&
              e.tool?.result?.status !== "error",
          )
        ) {
          changes.push({
            id: JSON.stringify([binding.id, upstream]),
            binding,
            model: upstream,
            upstream,
            action: "add",
            reasons: ["检测可用，未发现单价、模型匹配或能力异常"],
            checkedAt,
          });
        }
      }
    }
  }
  return { changes, notes: [...notes] };
}

// Derive final memberships first, preserve surviving order, append additions.
// Only selected changes enter the request; aliases and unrelated models survive.
export function optimizationRequests(
  changes: OptimizationChange[],
  routes: Record<string, Routes>,
) {
  const bindings = new Map<
    string,
    { binding: OptimizationBinding; models: Record<string, string> }
  >();
  for (const c of changes) {
    const item = bindings.get(c.binding.id) || {
      binding: c.binding,
      models: { ...c.binding.current },
    };
    if (c.action === "add") item.models[c.model] = c.upstream;
    else delete item.models[c.model];
    bindings.set(c.binding.id, item);
  }
  const orders = new Map<string, string[]>();
  const orderID = (source: string, key: string, model: string) =>
    JSON.stringify([source, key, model]);
  for (const [source, data] of Object.entries(routes))
    for (const r of data.data) {
      const id = orderID(source, r.api_key_id, r.model),
        list = orders.get(id) || [];
      if (!list.includes(r.provider)) list.push(r.provider);
      orders.set(id, list);
    }
  for (const { binding: b, models } of bindings.values())
    for (const model of Object.keys(b.current))
      if (!(model in models)) {
        const id = orderID(b.source, b.key, model);
        orders.set(
          id,
          (orders.get(id) || []).filter((p) => p !== b.provider),
        );
      }
  for (const { binding: b, models } of bindings.values())
    for (const model of Object.keys(models)) {
      const id = orderID(b.source, b.key, model),
        list = orders.get(id) || [];
      if (!list.includes(b.provider)) list.push(b.provider);
      orders.set(id, list);
    }
  const requests = new Map<
    string,
    {
      revision: string;
      part: "optimize";
      targets: {
        api_key_id: string;
        provider: string;
        account_id?: string;
        group_id?: number;
        origin_provider?: string;
        current: Record<string, string>;
        models: Record<string, string>;
        positions: Record<string, number>;
      }[];
    }
  >();
  for (const { binding: b, models } of bindings.values()) {
    const request = requests.get(b.source) || {
      revision: b.revision,
      part: "optimize" as const,
      targets: [],
    };
    if (request.revision !== b.revision)
      throw Error("来源配置版本不一致，请重新分析");
    request.targets.push({
      api_key_id: b.key,
      provider: b.provider,
      ...(b.installed
        ? { account_id: b.installed.account_id, group_id: b.installed.group_id }
        : { origin_provider: b.native!.provider }),
      current: b.current,
      models,
      positions: Object.fromEntries(
        Object.keys(models).map((model) => [
          model,
          (orders.get(orderID(b.source, b.key, model)) || []).indexOf(
            b.provider,
          ) + 1,
        ]),
      ),
    });
    requests.set(b.source, request);
  }
  return requests;
}
export async function applyOptimization(
  changes: OptimizationChange[],
  routes: Record<string, Routes>,
  progress: (
    source: string,
    state: "running" | "done" | "error",
    message?: string,
  ) => void,
) {
  await Promise.all(
    [...optimizationRequests(changes, routes)].map(async ([source, body]) => {
      progress(source, "running");
      try {
        const result = await controlRequest<{ revision: string }>(
          `/v1/sources/${encodeURIComponent(source)}/channel-batch`,
          {
            method: "POST",
            signal: AbortSignal.timeout(120000),
            body: JSON.stringify(body),
          },
        );
        if (!result.revision) throw Error("未取得应用版本，请刷新核对实际结果");
        progress(source, "done");
      } catch (e) {
        progress(
          source,
          "error",
          e instanceof Error ? e.message : "应用未确认，请刷新核对",
        );
      }
    }),
  );
}
