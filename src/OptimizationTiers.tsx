import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { channelMembers } from "./channelManagement";
import type { OptimizationSnapshot } from "./channelOptimizationPlan";
import { boundGroups, type InstalledChannel } from "./sub2apiImports";
import { serviceTierName, serviceTiers, type ServiceTier } from "./serviceTierStatus";
import { tierIndex, tierMemberKey, tierRecord, tierResultStatus, tierTarget } from "./serviceTierTable";
import "./serviceTierRouting.css";

type TierChannel = { member: InstalledChannel; configuration: NonNullable<InstalledChannel["service_tier_routing"]>; verified: Set<ServiceTier> };
export function optimizationTierChannels(snapshot: OptimizationSnapshot): TierChannel[] {
  const eligible = new Map<string, InstalledChannel>();
  const scopedGroups = new Set(snapshot.rows.filter(r => !r.configured).map(r => JSON.stringify([r.account.id, r.target.group_id])));
  for (const row of snapshot.rows) {
    if (!row.configured) continue;
    for (const member of channelMembers(row.configured)) eligible.set(tierMemberKey(member), member);
  }
  for (const member of snapshot.imports.data) {
    if (boundGroups(member).some(g => scopedGroups.has(JSON.stringify([g.account_id, g.group_id])))) {
      eligible.set(tierMemberKey(member), member);
    }
  }
  const checks = tierIndex(snapshot.checks);
  return [...eligible.values()].flatMap(member => {
    const route = snapshot.routes[member.source_id];
    const configuration = member.service_tier_routing;
    if (!configuration || !member.service_tier_scope || !route?.revision || !route.snapshot_consistent ||
        (member.engine !== "gpt" && member.engine !== "codex")) return [];
    const verified = new Set<ServiceTier>();
    for (const model of member.models) {
      const target = tierTarget(member, model);
      if (!target) continue;
      for (const tier of serviceTiers) {
        if (tierResultStatus(tierRecord(checks, tier, target), target) === "supported") verified.add(tier);
      }
    }
    return [{member, configuration, verified}];
  });
}

export function OptimizationTiers({snapshot, disabled, onBusy, onSaved}: {
  snapshot: OptimizationSnapshot; disabled?: boolean; onBusy: (value: boolean) => void; onSaved: () => void;
}) {
  const client = useQueryClient();
  const channels = useMemo(() => optimizationTierChannels(snapshot), [snapshot]);
  const [choices, setChoices] = useState<Record<string, boolean>>({});
  const [notice, setNotice] = useState<{text: string; error: boolean}>();
  const [finished, setFinished] = useState(false);
  const running = useRef(false);
  const changes = channels.flatMap(current => serviceTiers.flatMap(tier => {
    const value = choices[`${tierMemberKey(current.member)}:${tier}`];
    return value !== undefined && value !== (current.configuration[tier] === "enabled")
      ? [{member: current.member, tier, value}] : [];
  }));
  async function save() {
    if (disabled || finished || running.current || !changes.length) return;
    running.current = true;
    onBusy(true);
    setNotice(undefined);
    try {
      const bySource = new Map<string, (typeof changes)>();
      for (const change of changes) {
        const source = change.member.source_id;
        const list = bySource.get(source) || [];
        list.push(change);
        bySource.set(source, list);
      }
      const results = await Promise.all([...bySource].map(async ([source, items]) => {
        const name = items[0].member.source_name || source;
        if (items.length > 64) return {name, error: "单个来源最多保存 64 项档位变更，请缩小筛选范围"};
        try {
          const receipt = await controlRequest<{status: string; message?: string}>(
            `/v1/sources/${encodeURIComponent(source)}/service-tier-routing`, {
              method: "POST", body: JSON.stringify({
                revision: snapshot.routes[source].revision,
                operation_id: crypto.randomUUID(),
                choices: items.map(({member, tier, value}) => ({
                  provider: member.provider, scope: member.service_tier_scope, tier, enabled: value,
                })),
              }),
            },
          );
          return {name, error: receipt.status !== "applied" ? receipt.message || "保存未确认" : ""};
        } catch (error) {
          return {name, error: error instanceof Error ? error.message : "保存未确认"};
        }
      }));
      const failures = results.filter(result => result.error);
      setNotice(failures.length
        ? {text: failures.map(result => `${result.name} · ${result.error}`).join("；"), error:true}
        : {text: `已保存 ${changes.length} 项档位变更`, error:false});
      setFinished(true);
      onSaved();
      void Promise.all(["management-query", "channel-management", "service-tier-members", "channel-routes", "sub2api-imports"]
        .map(key => client.invalidateQueries({queryKey:[key]}, {cancelRefetch:false})));
    } finally {
      running.current = false;
      onBusy(false);
    }
  }
  return <details className="opt-tier-settings">
    <summary>Fast / Ultrafast</summary>
    {channels.length ? <>
      <div className="table-scroll">
        <table>
          <thead><tr><th>渠道</th><th>Fast</th><th>Ultrafast</th></tr></thead>
          <tbody>{channels.map(({member, configuration, verified}) =>
            <tr key={tierMemberKey(member)}>
              <td><strong>{member.name || member.provider}</strong> <small>{member.source_name}</small></td>
              {serviceTiers.map(tier => {
                const id = `${tierMemberKey(member)}:${tier}`;
                const current = configuration[tier];
                const editable = current === "enabled" || current === "disabled_verified" || verified.has(tier);
                return <td key={tier}><input type="checkbox"
                  aria-label={`${member.source_name} ${member.name || member.provider} ${serviceTierName[tier]}`}
                  title={editable ? serviceTierName[tier] : "请先验证此档位"}
                  disabled={disabled || finished || !editable}
                  checked={choices[id] ?? current === "enabled"}
                  onChange={event => setChoices(old => ({...old, [id]:event.target.checked}))}/></td>;
              })}
            </tr>)}</tbody>
        </table>
      </div>
      <button type="button" className="button small" disabled={disabled || finished || !changes.length} onClick={() => void save()}>保存档位</button>
      {notice && <p role={notice.error ? "alert" : "status"} className={notice.error ? "negative" : "positive"}>{notice.text}</p>}
    </> : <p className="muted">当前筛选中没有可配置档位的已接入渠道</p>}
  </details>;
}
