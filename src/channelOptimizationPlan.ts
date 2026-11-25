import { controlRequest } from "./api";
import {
  channelProtocolLabel,
  channelProtocols,
  protocolForEngine,
} from "./channelProtocol";
import { fingerprintKey, sharedFingerprintIndex } from "./fingerprintResults";
import type { FingerprintRecord, FingerprintResult } from "./ModelFingerprints";
import {
  optimizationTierChanges,
  tierOptimizationEvents,
} from "./channelOptimizationTiers";
import type { ServiceTier } from "./serviceTierStatus";
import { billingRate, matchesBillingRateFilter } from "./billingRates";
import { boundGroups } from "./sub2apiImports";
import { isCapabilityCheckModel } from "./sub2apiModels";
import type { InstalledChannel, SubImports } from "./sub2apiImports";
import {
  channelMembers,
  configuredUpstreamModelChecks,
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
  modelIsDefaultSelected,
  modelMatchStatus,
  groupQualityResult,
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { assessPrice, dollars, matchesPriceFilter } from "./sub2apiPriceCheck";
import { toolUseModels } from "./toolUse";
import type { ToolUseResult, ToolUseModel } from "./toolUse";
import type { CompactionResult } from "./SubCompaction";
import type { ModelPrice } from "./types";

export const optimizationEvents = [
  { id: "add", label: "新增可用模型" },
  { id: "legacy", label: "升级旧版默认协议" },
  { id: "protocol", label: "按检测协议接入模型" },
  { id: "protocol-switch", label: "切换已接入模型的协议" },
  { id: "availability", label: "取消检测失败的模型" },
  { id: "tool", label: "取消不支持 Tool use 的模型" },
  { id: "price", label: "取消单价异常的模型" },
  { id: "match", label: "取消模型不匹配的模型" },
  { id: "fingerprint", label: "取消指纹不匹配的模型" },
  { id: "quality", label: "取消降智的 Astra 模型" },
  { id: "compaction", label: "取消不支持压缩的已检测模型" },
  ...tierOptimizationEvents,
] as const;
export type OptimizationEvent = (typeof optimizationEvents)[number]["id"];
export interface OptimizationSnapshot {
  rows: ManagementRow[];
  accounts: SubAccount[];
  inventory: ManagedChannel[];
  imports: SubImports;
  routes: Record<string, Routes>;
  checks: ConfiguredCheck[];
  fingerprints?: FingerprintRecord[];
  fingerprintReference?: string;
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
  engine?: string;
  engineMode?: string;
  protocol?: string;
  engineOrigin?: string;
  legacyEngineEligible?: boolean;
}
export interface OptimizationChange {
  id: string;
  binding: OptimizationBinding;
  model: string;
  upstream: string;
  action: "add" | "remove" | "switch" | "migrate" | "tier-add" | "tier-remove";
  reasons: string[];
  returnedModels?: string[];
  priceDetails?: string[];
  fingerprintDetails?: {
    id: string;
    scope: string;
    probability: FingerprintResult["probability"];
    candidates: FingerprintResult["candidates"];
  }[];
  checkedAt: number;
  detectedProtocols?: string[];
  blockedReason?: string;
  requiresLegacyMigration?: boolean;
  splitProtocol?: string;
  reuseBinding?: OptimizationBinding;
  protocolMove?: {
    protocol: string;
    provider: string;
  };
  tier?: ServiceTier;
  tierScope?: string;
  tierEvidenceModels?: string[];
}
export interface OptimizationPlan {
  changes: OptimizationChange[];
  notes: string[];
}
export const optimizationTarget = (change: OptimizationChange) =>
  change.reuseBinding ?? change.binding;
const providerID = (source: string, provider: string) =>
  JSON.stringify([source, provider]);
const bindingID = (source: string, key: string, provider: string) =>
  JSON.stringify([source, key, provider]);
const protocolRoot = (provider: string) =>
  provider.replace(/--(?:responses|messages|gemini|chat)$/, "");
const protocolSibling = (provider: string, protocol: string) =>
  `${protocolRoot(provider)}--${protocol}`;

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
  fingerprint?: FingerprintRecord;
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
  fingerprints: Map<string, FingerprintRecord>,
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
      fingerprint: fingerprints.get(
        fingerprintKey({
          account_id: siteGroups[i].account_id,
          group_id: siteGroups[i].group_id,
          model: upstream,
        }),
      ),
      tool: siteTool(target, upstream),
      compaction: target?.compaction,
      compactionState: target?.compaction_state,
      degraded:
        upstream === "gpt-6-astra" &&
        !!target &&
        groupQualityResult(target)?.verdict === "fail",
    }));
  const member = binding.native;
  const fingerprintTarget = fingerprintKey({
    source_id: member.source_id,
    provider: member.provider,
    model: upstream,
  });
  // A different caller-key copy or credential must not inherit a removal.
  const fingerprint =
    binding.provider === member.provider &&
    (member.model_mappings?.[upstream] || upstream) === upstream
      ? fingerprints.get(fingerprintTarget)
      : undefined;
  const native = snapshot.checks.filter(
    (c) =>
      c.source_id === member.source_id &&
      c.provider === member.provider &&
      (c.fingerprint || "") === (member.probe_fingerprint || ""),
  );
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
    const checks = configuredUpstreamModelChecks(
      scoped,
      snapshot.accounts,
      native,
      [upstream],
    );
    const check = checks[0];
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
      fingerprint:
        fingerprint &&
        (fingerprintKey(fingerprint) !== fingerprintTarget ||
          (!!member.probe_fingerprint &&
            fingerprint.revision === member.probe_fingerprint))
          ? fingerprint
          : undefined,
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
  const billing = billingRate(e.target, c.model);
  return (
    (!filters.accountId ||
      (filters.accountId === "__unassigned__"
        ? !e.accountId
        : filters.accountId === e.accountId)) &&
    matchesBillingRateFilter(billing, filters.maxRate) &&
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
function fingerprintMismatch(record?: FingerprintRecord) {
  const result = record?.result;
  if (
    record?.state === "done" &&
    result?.status === "mismatch" &&
    result.decision !== "not_model" &&
    result.used_outputs >= 3
  )
    return result;
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
    if (hasModelMismatch(e.check)) result.push(["match", "返回模型不匹配"]);
    if (assessPrice(e.check, prices).status === "abnormal")
      result.push(["price", "单价异常"]);
  }
  if (fingerprintMismatch(e.fingerprint))
    result.push(["fingerprint", "模型指纹不匹配"]);
  if (
    isCapabilityCheckModel(model) &&
    !running(e.tool?.state) &&
    e.tool?.result?.status === "unsupported"
  )
    result.push(["tool", "不支持 Tool use"]);
  if (e.degraded && !running(e.check.state))
    result.push(["quality", "Astra 最近检测降智"]);
  if (
    isCapabilityCheckModel(model) &&
    !running(e.compactionState) &&
    e.compaction?.model === model &&
    e.compaction.status === "unsupported"
  )
    result.push(["compaction", "该模型不支持远程压缩"]);
  return result;
}

function hasModelMismatch(check: SubModelCheck) {
  const status = modelMatchStatus(check);
  return status === "mismatch" || status === "invalid";
}

export function buildOptimizationPlan(
  snapshot: OptimizationSnapshot,
  events: OptimizationEvent[],
): OptimizationPlan {
  const notes = new Set<string>(),
    changes: OptimizationChange[] = [];
  const fingerprints = sharedFingerprintIndex(
    (snapshot.fingerprints || []).map((record) =>
      snapshot.fingerprintReference &&
      record.result?.reference_commit !== snapshot.fingerprintReference
        ? { ...record, result: null }
        : record,
    ),
    snapshot.inventory,
    [],
    snapshot.imports.data,
    snapshot.imports.unavailable_sources,
  );
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
    const splitClaims = new Map<string, string>();
    const routesByBinding = new Map<string, Routes["data"]>();
    const occupiedProviders = new Set([
      ...snapshot.imports.data
        .filter((item) => item.source_id === source)
        .map((item) => item.provider),
      ...routes.data.map((route) => route.provider),
    ]);
    for (const route of routes.data) {
      const id = bindingID(source, route.api_key_id, route.provider);
      const routeRows = routesByBinding.get(id);
      if (routeRows) routeRows.push(route);
      else routesByBinding.set(id, [route]);
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
    // Resolve every binding before suggesting changes. A sibling can appear
    // after the original channel in the caller-key catalog.
    const validBindings = new Map<string, OptimizationBinding>();
    for (const binding of bindings.values()) {
      const routeRows = routesByBinding.get(binding.id)!;
      const engines = [
        ...new Set(
          routeRows.map((r) => r.engine).filter((v): v is string => !!v),
        ),
      ];
      // The caller-key catalog is authoritative. Never infer a protocol from a
      // model name or a provider suffix; copies can have independent overrides.
      binding.engine =
        engines.length === 1
          ? engines[0]
          : engines.length
            ? undefined
            : (binding.installed || binding.native)?.engine;
      binding.engineMode =
        routeRows.find((r) => r.engine_mode)?.engine_mode ||
        (binding.installed || binding.native)?.engine_mode;
      binding.protocol = protocolForEngine(binding.engine);
      const origins = [
        ...new Set(routeRows.map((r) => r.engine_origin || "unknown")),
      ];
      binding.engineOrigin = origins.length === 1 ? origins[0] : "unknown";
      binding.legacyEngineEligible =
        !!binding.installed &&
        routes.legacy_engine_migration === true &&
        binding.engine === "gpt" &&
        binding.engineOrigin === "legacy_default";
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
      validBindings.set(binding.id, binding);
    }
    for (const binding of validBindings.values()) {
      const groups = boundGroups(binding.installed || binding.native);
      const migrate = binding.legacyEngineEligible && events.includes("legacy");
      if (migrate)
        changes.push({
          id: JSON.stringify([binding.id, "legacy-engine", "migrate"]),
          binding,
          model: "",
          upstream: "",
          action: "migrate",
          checkedAt: 0,
          reasons: [
            "删除旧版自动写入的 gpt，引擎改由现有 Responses 地址识别；已有模型、映射和请求顺序保留",
          ],
        });
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
        const evidence = evidenceFor(binding, upstream, snapshot, fingerprints);
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
        const filteredEvidence = evidence.filter((e) =>
          modelMatchesFilters(e, snapshot.filters, snapshot.prices),
        );
        const reasons = [
          ...new Set(
            filteredEvidence
              .flatMap((e) => faults(e, upstream, snapshot.prices))
              .filter(([event]) => events.includes(event))
              .map(([, reason]) => reason),
          ),
        ];
        const returnedModels = events.includes("match")
          ? [
              ...new Set(
                filteredEvidence.flatMap(({ check }) => {
                  if (running(check.state) || !hasModelMismatch(check))
                    return [];
                  const model =
                    check.result?.availability.response_model?.trim();
                  return model ? [model] : [];
                }),
              ),
            ]
          : [];
        const priceDetails = events.includes("price")
          ? [
              ...new Set(
                filteredEvidence
                  .filter((e) => !running(e.check.state))
                  .map((e) => assessPrice(e.check, snapshot.prices))
                  .filter((price) => price.status === "abnormal")
                  .map(
                    ({ expected, usage }) =>
                      `输入：应为 ${dollars(expected?.input)}，实际 ${dollars(usage?.input_price)}；输出：应为 ${dollars(expected?.output)}，实际 ${dollars(usage?.output_price)}`,
                  ),
              ),
            ]
          : [];
        const fingerprintDetails = new Map<
          string,
          NonNullable<OptimizationChange["fingerprintDetails"]>[number]
        >();
        if (events.includes("fingerprint") && publicModels.length)
          for (const e of filteredEvidence) {
            const record = e.fingerprint;
            const mismatch = fingerprintMismatch(record);
            if (!mismatch || !record) continue;
            const id = fingerprintKey(record);
            if (fingerprintDetails.has(id)) continue;
            const account = snapshot.accounts.find(
              (a) => a.id === record.account_id,
            );
            const group = account?.targets.find(
              (t) => t.group_id === record.group_id,
            );
            fingerprintDetails.set(id, {
              id,
              scope:
                [account?.name, group?.name].filter(Boolean).join(" · ") ||
                record.provider ||
                binding.name,
              probability: mismatch.probability,
              candidates: mismatch.candidates ?? [],
            });
          }
        const checkedAt = Math.max(
          0,
          ...evidence.map((e) => e.check.result?.checked_at || 0),
          ...evidence.map((e) => e.tool?.result?.checked_at || 0),
          ...evidence.map((e) => e.fingerprint?.result?.checked_at || 0),
        );
        const detectedProtocols = [
          ...new Set(
            evidence.flatMap((e) =>
              e.check.result?.availability.status === "success" &&
              e.check.result.availability.protocol
                ? [e.check.result.availability.protocol]
                : [],
            ),
          ),
        ];
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
                returnedModels,
                priceDetails,
                fingerprintDetails: [...fingerprintDetails.values()],
                checkedAt,
                detectedProtocols,
              });
            }
        }
        const canConnect =
          !problems.length &&
          evidence.every(
            (e) =>
              modelMatchesFilters(e, snapshot.filters, snapshot.prices) &&
              e.canAdd &&
              e.check.state === "done" &&
              modelIsDefaultSelected(e.check) &&
              (!isCapabilityCheckModel(upstream) ||
                (!running(e.tool?.state) &&
                  e.tool?.result?.status !== "error")),
          );
        if (!canConnect) continue;
        const effectiveProtocol = migrate ? "responses" : binding.protocol;
        const conflicting =
          !!binding.installed &&
          !!effectiveProtocol &&
          detectedProtocols.some((protocol) => protocol !== effectiveProtocol);
        const detected =
          detectedProtocols.length === 1 &&
          channelProtocols.find((p) => p.protocol === detectedProtocols[0]);
        const switching = publicModels.length > 0;
        if (
          switching
            ? !events.includes("protocol-switch") ||
              !routes.optimize_protocol_moves ||
              !conflicting
            : !events.includes("add") ||
              Object.hasOwn(binding.current, upstream)
        )
          continue;
        for (const model of switching ? publicModels : [upstream]) {
          if (switching && scope && scope !== upstream && scope !== model)
            continue;
          let splitProtocol: string | undefined;
          let reuseBinding: OptimizationBinding | undefined;
          let protocolMove: OptimizationChange["protocolMove"];
          let conflictDetail = "请在添加到渠道中按协议拆分";
          if (
            switching &&
            (binding.engineMode !== "auto" ||
              binding.engineOrigin === "user_override")
          )
            conflictDetail = "当前接入使用显式配置或人工覆盖，不能自动切换";
          if (
            conflicting &&
            detected &&
            (switching || events.includes("protocol")) &&
            routes.optimize_extended &&
            routes.legacy_engine_migration &&
            binding.installed?.manageable &&
            binding.engineMode === "auto" &&
            binding.engineOrigin !== "user_override"
          ) {
            const root = protocolRoot(binding.provider);
            const sibling = protocolSibling(
              binding.provider,
              detected.protocol,
            );
            const siblingID = bindingID(source, binding.key, sibling);
            const rootID = bindingID(source, binding.key, root);
            const compatible = [siblingID, rootID]
              .map((id) => validBindings.get(id))
              .filter(
                (target): target is OptimizationBinding =>
                  !!target?.installed?.manageable &&
                  target.id !== binding.id &&
                  target.installed.account_id ===
                    binding.installed!.account_id &&
                  target.installed.group_id === binding.installed!.group_id &&
                  (target.legacyEngineEligible &&
                  events.includes("legacy") &&
                  (!switching || target.current[model] !== upstream)
                    ? "responses"
                    : target.protocol) === detected.protocol,
              );
            // Match normal additions: do not add another public name for an
            // upstream already served, or replace an existing alias.
            if (
              !switching &&
              compatible.some((target) =>
                Object.values(target.current).includes(upstream),
              )
            )
              continue;
            reuseBinding =
              (switching
                ? compatible.find(
                    (target) => target.current[model] === upstream,
                  )
                : undefined) ??
              compatible.find(
                (target) => !Object.hasOwn(target.current, model),
              );
            if (compatible.length && !reuseBinding) {
              conflictDetail = "目标协议接入已使用同名模型，不能覆盖现有映射";
            } else if (
              !reuseBinding &&
              (occupiedProviders.has(sibling) ||
                (rootID !== binding.id &&
                  (allowedSite.get(rootID)?.protocol === detected.protocol ||
                    routesByBinding
                      .get(rootID)
                      ?.some(
                        (r) =>
                          protocolForEngine(r.engine) === detected.protocol,
                      ))))
            ) {
              conflictDetail =
                "无法确认已有目标协议接入的完整配置，请刷新渠道管理";
            } else if (
              !reuseBinding &&
              splitClaims.has(siblingID) &&
              splitClaims.get(siblingID) !== binding.id
            ) {
              conflictDetail = "已在同一 Key 的另一接入中列出该协议变更";
            } else if (!reuseBinding) {
              splitClaims.set(siblingID, binding.id);
              splitProtocol = detected.protocol;
            }
            if (switching && (reuseBinding || splitProtocol))
              protocolMove = {
                protocol: detected.protocol,
                provider: reuseBinding?.provider || sibling,
              };
          }
          changes.push({
            id: JSON.stringify([binding.id, model]),
            binding,
            model,
            upstream,
            action: switching ? "switch" : "add",
            reasons: [
              switching
                ? reuseBinding
                  ? "移除该模型的旧协议路由，复用同一 Key 的目标接入"
                  : splitProtocol
                    ? "为该模型建立检测通过的协议路由，同时移除旧路由"
                    : "检测通过的协议与当前接入不同"
                : reuseBinding
                  ? "新增到同一 Key 下已有的协议接入；保留原协议和现有模型"
                  : splitProtocol
                    ? "按检测协议自动创建接入；保留原协议和现有模型"
                    : "检测可用，未发现单价、模型匹配、指纹或能力异常",
            ],
            checkedAt,
            detectedProtocols,
            requiresLegacyMigration: reuseBinding
              ? !!reuseBinding.legacyEngineEligible && events.includes("legacy")
              : !!migrate && !splitProtocol && !switching,
            splitProtocol,
            reuseBinding,
            protocolMove,
            blockedReason: binding.installed
              ? !binding.protocol
                ? "当前渠道协议未确认，请刷新或检查渠道配置"
                : !detectedProtocols.length
                  ? "检测未记录上游协议，请先重新检测该模型"
                  : conflicting && !splitProtocol && !reuseBinding
                    ? `协议冲突：当前 ${channelProtocolLabel(binding.protocol)}，检测 ${detectedProtocols.map(channelProtocolLabel).join(" / ")}；${conflictDetail}`
                    : undefined
              : undefined,
          });
        }
      }
    }
  }
  // Prefer the selectable suggestion on the original conflict card over a
  // second copy on the destination card. One model write, one selection.
  const additions = new Map<string, OptimizationChange>();
  const destinationID = (c: OptimizationChange) => {
    const b = c.binding;
    const provider =
      c.protocolMove?.provider ??
      (c.splitProtocol
        ? protocolSibling(b.provider, c.splitProtocol)
        : optimizationTarget(c).provider);
    return JSON.stringify([bindingID(b.source, b.key, provider), c.model]);
  };
  for (const change of changes) {
    if (
      (change.action !== "add" && change.action !== "switch") ||
      change.blockedReason
    )
      continue;
    const id = destinationID(change);
    const prev = additions.get(id);
    if (
      !prev ||
      (prev.action !== "switch" &&
        (change.action === "switch" ||
          (change.reuseBinding && !prev.reuseBinding)))
    )
      additions.set(id, change);
  }
  return {
    changes: [
      ...changes.filter(
        (c) =>
          (c.action !== "add" && c.action !== "switch") ||
          !!c.blockedReason ||
          additions.get(destinationID(c)) === c,
      ),
      ...optimizationTierChanges(snapshot, events),
    ],
    notes: [...notes],
  };
}

// Derive final memberships first, preserve surviving order, append additions.
// Only selected changes enter the request; aliases and unrelated models survive.
export function optimizationRequests(
  changes: OptimizationChange[],
  routes: Record<string, Routes>,
) {
  const blocked = changes.find((c) => c.blockedReason);
  if (blocked)
    throw Error(
      `${blocked.binding.name} · ${blocked.model}：${blocked.blockedReason}`,
    );
  const bindings = new Map<
    string,
    {
      binding: OptimizationBinding;
      models: Record<string, string>;
      migrate: boolean;
      splitProtocols: Record<string, string>;
    }
  >();
  const migrations = new Set(
    changes.filter((c) => c.action === "migrate").map((c) => c.binding.id),
  );
  const membership = (target: OptimizationBinding) => {
    const item = bindings.get(target.id) || {
      binding: target,
      models: { ...target.current },
      migrate: migrations.has(target.id),
      splitProtocols: {},
    };
    bindings.set(target.id, item);
    return item;
  };
  for (const c of changes) {
    if (c.tier) continue;
    const target = optimizationTarget(c);
    if (c.requiresLegacyMigration && !migrations.has(target.id))
      throw Error(`${target.name}：请同时勾选旧版默认协议升级`);
    const item = membership(target);
    if (c.action === "add") {
      item.models[c.model] = c.upstream;
      if (c.splitProtocol) item.splitProtocols[c.model] = c.splitProtocol;
    } else if (c.action === "remove") delete item.models[c.model];
    else if (c.action === "switch") {
      if (!c.protocolMove || (!c.splitProtocol && !c.reuseBinding))
        throw Error("目标协议未确认，请重新读取优化预览");
      if (c.splitProtocol) {
        item.splitProtocols[c.model] = c.splitProtocol;
      } else {
        delete membership(c.binding).models[c.model];
        item.models[c.model] = c.upstream;
      }
    }
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
  for (const c of changes) {
    if (!c.protocolMove) continue;
    const b = c.binding;
    const id = orderID(b.source, b.key, c.model);
    const list = orders.get(id);
    if (!list?.includes(b.provider))
      throw Error("模型路由已变化，请重新读取优化预览");
    const destination = c.protocolMove.provider;
    orders.set(
      id,
      list.includes(destination)
        ? list.filter((p) => p !== b.provider)
        : list.map((p) => (p === b.provider ? destination : p)),
    );
  }
  for (const { binding: b, models, splitProtocols } of bindings.values())
    for (const model of Object.keys(b.current))
      if (!Object.hasOwn(models, model) || splitProtocols[model]) {
        const id = orderID(b.source, b.key, model);
        orders.set(
          id,
          (orders.get(id) || []).filter((p) => p !== b.provider),
        );
      }
  for (const { binding: b, models, splitProtocols } of bindings.values())
    for (const model of Object.keys(models)) {
      const id = orderID(b.source, b.key, model),
        list = orders.get(id) || [];
      const provider = splitProtocols[model]
        ? protocolSibling(b.provider, splitProtocols[model])
        : b.provider;
      if (!list.includes(provider)) list.push(provider);
      orders.set(id, list);
    }
  type Request = {
    revision: string;
    part: "optimize" | "optimize_protocol";
    protocol_moves?: {
      api_key_id: string;
      from_provider: string;
      to_provider: string;
      model: string;
      protocol: string;
    }[];
    tier_choices?: {
      provider: string;
      scope: string;
      tier: ServiceTier;
      enabled: boolean;
      evidence_models: string[];
    }[];
    targets: {
      api_key_id: string;
      provider: string;
      account_id?: string;
      group_id?: number;
      origin_provider?: string;
      current: Record<string, string>;
      models: Record<string, string>;
      positions: Record<string, number>;
      migrate_legacy_engine?: boolean;
      split_protocols?: Record<string, string>;
    }[];
  };
  const requests = new Map<string, Request>();
  for (const {
    binding: b,
    models,
    migrate,
    splitProtocols,
  } of bindings.values()) {
    const request: Request = requests.get(b.source) || {
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
      ...(migrate &&
      !Object.keys(splitProtocols).length &&
      Object.keys(models).length
        ? { migrate_legacy_engine: true }
        : {}),
      ...(Object.keys(splitProtocols).length
        ? { split_protocols: splitProtocols }
        : {}),
      models,
      positions: Object.fromEntries(
        Object.keys(models).map((model) => [
          model,
          (orders.get(orderID(b.source, b.key, model)) || []).indexOf(
            splitProtocols[model]
              ? protocolSibling(b.provider, splitProtocols[model])
              : b.provider,
          ) + 1,
        ]),
      ),
    });
    requests.set(b.source, request);
  }
  for (const c of changes) {
    if (!c.protocolMove) continue;
    const request = requests.get(c.binding.source)!;
    // Older backends must reject a switch rather than apply only its removal.
    request.part = "optimize_protocol";
    (request.protocol_moves ??= []).push({
      api_key_id: c.binding.key,
      from_provider: c.binding.provider,
      to_provider: c.protocolMove.provider,
      model: c.model,
      protocol: c.protocolMove.protocol,
    });
  }
  for (const change of changes) {
    if (!change.tier || !change.tierScope || !change.tierEvidenceModels?.length)
      continue;
    const b = change.binding;
    const membership = bindings.get(b.id);
    if (
      membership &&
      !Object.keys(membership.models).some(
        (model) => !membership.splitProtocols[model],
      )
    )
      throw Error(
        `${b.name}：所选变更会移除原接入，请取消该接入的档位变更后继续`,
      );
    if (
      membership &&
      change.action === "tier-add" &&
      !change.tierEvidenceModels.some(
        (model) =>
          membership.models[model] === b.current[model] &&
          !!b.current[model] &&
          !membership.splitProtocols[model],
      )
    )
      throw Error(
        `${b.name}：已验证档位的模型将离开原接入，请取消该接入的档位变更`,
      );
    const request: Request = requests.get(b.source) || {
      revision: b.revision,
      part: "optimize" as const,
      targets: [],
    };
    if (request.revision !== b.revision)
      throw Error("来源配置版本不一致，请重新分析");
    (request.tier_choices ??= []).push({
      provider: b.provider,
      scope: change.tierScope,
      tier: change.tier,
      enabled: change.action === "tier-add",
      evidence_models: change.tierEvidenceModels,
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
        const result = await controlRequest<{
          revision: string;
          tier_changes?: number;
          protocol_moves?: number;
        }>(`/v1/sources/${encodeURIComponent(source)}/channel-batch`, {
          method: "POST",
          signal: AbortSignal.timeout(120000),
          body: JSON.stringify(body),
        });
        if (!result.revision) throw Error("未取得应用版本，请刷新核对实际结果");
        if (
          body.protocol_moves?.length &&
          result.protocol_moves !== body.protocol_moves.length
        )
          throw Error("协议切换未收到完整回执，请刷新核对实际结果");
        if (
          body.tier_choices?.length &&
          result.tier_changes !== body.tier_choices.length
        )
          throw Error("档位变更未收到完整回执，请刷新核对实际结果");
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
