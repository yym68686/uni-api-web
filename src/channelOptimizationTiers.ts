import { channelMembers } from "./channelManagement";
import type {
  OptimizationChange,
  OptimizationEvent,
  OptimizationSnapshot,
} from "./channelOptimizationPlan";
import { boundGroups, type InstalledChannel } from "./sub2apiImports";
import { protocolForEngine } from "./channelProtocol";
import {
  serviceTierFresh,
  serviceTierName,
  serviceTiers,
  serviceTierVerified,
  type ServiceTierResult,
} from "./serviceTierStatus";
import {
  tierIndex,
  tierMemberKey,
  tierRecord,
  tierTarget,
} from "./serviceTierTable";

export const tierOptimizationEvents = [
  { id: "fast-add", label: "新增 Fast" },
  { id: "fast-remove", label: "取消 Fast" },
  { id: "ultrafast-add", label: "新增 Ultrafast" },
  { id: "ultrafast-remove", label: "取消 Ultrafast" },
] as const;

export function optimizationTierChanges(
  snapshot: OptimizationSnapshot,
  events: OptimizationEvent[],
): OptimizationChange[] {
  const eligible = new Map<
    string,
    { member: InstalledChannel; models: Set<string> }
  >();
  const groups = new Set<string>();
  const { model, accountId } = snapshot.filters;
  const add = (member: InstalledChannel, site: boolean) => {
    const id = tierMemberKey(member);
    const entry = eligible.get(id) || { member, models: new Set<string>() };
    eligible.set(id, entry);
    for (const name of member.models) {
      const selectedName = site ? member.model_mappings?.[name] || name : name;
      if (!model || model === selectedName) entry.models.add(name);
    }
  };
  for (const row of snapshot.rows) {
    if (!row.configured)
      groups.add(JSON.stringify([row.account.id, row.target.group_id]));
    else
      for (const member of channelMembers(row.configured)) {
        if (
          !accountId ||
          (accountId === "__unassigned__"
            ? !member.account_ids?.length
            : member.account_id === accountId ||
              member.account_ids?.includes(accountId))
        )
          add(member, false);
      }
  }
  for (const item of snapshot.imports.data) {
    if (
      item.kind === "configured" ||
      snapshot.imports.unavailable_sources?.some(
        (id) => id === item.source_id || id === item.source_name,
      )
    )
      continue;
    if (
      boundGroups(item).some((g) =>
        groups.has(JSON.stringify([g.account_id, g.group_id])),
      )
    )
      add(item, true);
  }
  const checks = tierIndex(snapshot.checks);
  const changes: OptimizationChange[] = [];
  for (const { member, models } of eligible.values()) {
    const route = snapshot.routes[member.source_id];
    const scope = member.service_tier_scope;
    if (
      !scope ||
      !member.service_tier_routing ||
      !route?.revision ||
      !route.snapshot_consistent ||
      !route.manageable ||
      !route.atomic_batch ||
      !route.optimize_batch ||
      !route.optimize_extended ||
      route.unavailable_keys?.length
    )
      continue;
    const targets = member.models.flatMap(
      (name) => tierTarget(member, name) || [],
    );
    const current = Object.fromEntries(
      member.models.map((name) => [
        name,
        member.model_mappings?.[name] || name,
      ]),
    );
    const binding = {
      id: JSON.stringify([
        member.source_id,
        member.api_key_id || "",
        member.provider,
      ]),
      source: member.source_id,
      sourceName: member.source_name,
      key: member.api_key_id,
      keyPosition: member.key_position,
      provider: member.provider,
      name: member.name,
      current,
      revision: route.revision,
      engine: member.engine,
      engineMode: member.engine_mode,
      protocol: protocolForEngine(member.engine),
    };
    for (const tier of serviceTiers) {
      const setting = member.service_tier_routing[tier];
      if (!setting) continue;
      const enabled = setting === "enabled";
      if (!events.includes(enabled ? `${tier}-remove` : `${tier}-add`))
        continue;
      const evidence = targets.map((target) => {
        const record = tierRecord(checks, tier, target);
        const receipt =
          record?.state === "done" && record.fingerprint === target.scope
            ? (record.result as ServiceTierResult | null)
            : null;
        const fresh =
          !!receipt?.binding && serviceTierFresh(receipt.checked_at);
        const status =
          fresh &&
          (receipt.status === "unsupported" || receipt.status === "not_honored")
            ? receipt.status
            : fresh &&
                receipt.status === "supported" &&
                serviceTierVerified(receipt)
              ? "supported"
              : "unknown";
        return {
          model: target.model,
          status,
          checkedAt: receipt?.checked_at ?? 0,
        };
      });
      const relevant = evidence.filter((e) => models.has(e.model));
      if (!relevant.length) continue;
      if (
        enabled
          ? !evidence.every(
              (e) => e.status === "unsupported" || e.status === "not_honored",
            )
          : !relevant.some((e) => e.status === "supported")
      )
        continue;
      changes.push({
        id: JSON.stringify([binding.id, "tier", tier]),
        binding,
        model: serviceTierName[tier],
        upstream: serviceTierName[tier],
        tier,
        tierScope: scope,
        tierEvidenceModels: (enabled
          ? evidence
          : relevant.filter((e) => e.status === "supported")
        ).map((e) => e.model),
        action: enabled ? "tier-remove" : "tier-add",
        reasons: [
          enabled
            ? "接入的相关模型均明确不支持或未命中该档位"
            : "业务凭据已验证支持该档位",
        ],
        checkedAt: Math.max(...relevant.map((e) => e.checkedAt)),
      });
    }
  }
  return changes;
}
