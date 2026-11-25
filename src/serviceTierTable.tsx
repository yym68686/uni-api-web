import { useMemo } from "react";
import { useQuery, type QueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { channelMembers, type ConfiguredCheck, type ManagementRow } from "./channelManagement";
import type { InstalledChannel } from "./sub2apiImports";
import type { Channel } from "./types";
import { isServiceTier, serviceTierFilterLabels, serviceTierFresh, serviceTierName, serviceTierVerified,
  type ServiceTier, type ServiceTierResult } from "./serviceTierStatus";

export const tierMemberKey = (member: { source_id?: string; provider: string }) =>
  JSON.stringify([member.source_id, member.provider]);
export type TierTarget = {
  source_id: string;
  provider: string;
  model: string;
  scope: string;
};
export const tierTargetKey = ({ source_id, provider, model }: TierTarget) =>
  JSON.stringify([source_id, provider, model]);
export const tierPending = (c?: ConfiguredCheck) => c?.state === "queued" || c?.state === "running";
const checkKey = (kind: string, target: string) => kind + ":" + target;

export function tierIndex(checks: ConfiguredCheck[]) {
  return new Map(checks.filter(c => isServiceTier(c.kind)).map(c => [
    checkKey(c.kind, JSON.stringify([c.source_id, c.provider, c.model])), c,
  ]));
}
export const tierRecord = (index: ReturnType<typeof tierIndex>, tier: ServiceTier, target: TierTarget) =>
  index.get(checkKey(tier, tierTargetKey(target)));

export function tierTarget(member: InstalledChannel | undefined, model: string): TierTarget | undefined {
  if (!member?.service_tier_scope || !member.models.includes(model)) return;
  if (member.engine !== "gpt" && member.engine !== "codex") return;
  const upstream = member.model_mappings?.[model] || model;
  if (!/^(gpt|codex)(?:$|[._/-])/i.test(upstream)) return;
  return {source_id: member.source_id, provider: member.provider, model, scope: member.service_tier_scope};
}

export function observedTierTarget(row: Channel, member?: InstalledChannel) {
  if (!member || row.upstream_model && row.upstream_model !== (member.model_mappings?.[row.model] || row.model)) return;
  return tierTarget(member, row.model);
}

export function managementTierTargets(row: ManagementRow, imports: InstalledChannel[], model?: string, account?: string) {
  const members = row.configured
    ? channelMembers(row.configured).filter(m => !account || m.account_id === account || m.account_ids?.includes(account) ||
        account === "__unassigned__" && !m.account_ids?.length)
    : imports.filter(m => m.kind !== "configured" && m.account_id === row.account.id && m.group_id === row.target.group_id);
  return members.flatMap(member => member.models.flatMap(name => {
    const matches = !model || model === (row.configured ? name : member.model_mappings?.[name] || name);
    const target = matches ? tierTarget(member, name) : undefined;
    return target ? [target] : [];
  }));
}

export function tierResultStatus(check: ConfiguredCheck | undefined, target: TierTarget) {
  const result = check?.fingerprint === target.scope ? check.result as ServiceTierResult | null : null;
  if (serviceTierVerified(result)) return "supported";
  if (result?.binding && serviceTierFresh(result.checked_at) &&
      (result.status === "unsupported" || result.status === "not_honored")) return result.status;
  return tierPending(check) ? "pending" : "unknown";
}

export function siteTierStatus(result?: {status: string; checked_at: number}) {
  if (serviceTierFresh(result?.checked_at) &&
    (result?.status === "supported" || result?.status === "unsupported" || result?.status === "not_honored"))
    return result.status;
}

export function ServiceTierCell({tier, targets, index, loading, failed, siteStatuses = []}: {
  tier: ServiceTier; targets: TierTarget[]; index: ReturnType<typeof tierIndex>; loading?: boolean; failed?: boolean;
  siteStatuses?: NonNullable<ReturnType<typeof siteTierStatus>>[];
}) {
  if (!siteStatuses.length && targets.length > 0 && (failed || loading)) return <span className="muted">{failed ? "读取失败" : "读取中…"}</span>;
  const statuses = siteStatuses.length ? siteStatuses : targets.map(target => tierResultStatus(tierRecord(index, tier, target), target));
  const detail = siteStatuses.length ? " · 站点检测 Key，不代表已接入渠道的业务凭据" : "";
  const verified = statuses.filter(s => s === "supported").length;
  if (verified && verified !== statuses.length)
    return <span className="positive" title={`${verified}/${statuses.length} 个渠道模型已验证支持${detail}`}>部分支持 · {verified}/{statuses.length}</span>;
  const status = statuses.length && statuses.every(s => s === statuses[0]) ? statuses[0] :
    statuses.includes("pending") ? "pending" : "unknown";
  const label = status === "pending" ? "检测中" : serviceTierFilterLabels[status];
  return <span className={status === "supported" ? "positive" : status === "unsupported" ? "negative" : "muted"}
    title={`${serviceTierName[tier]} · ${label}${detail}`}>{label}</span>;
}

export function useTierChecks(owner: string, enabled: boolean) {
  const query = useQuery({
    queryKey: ["service-tier-table", owner],
    queryFn: ({signal}) => controlRequest<{data: ConfiguredCheck[]}>("/v1/channel-management/checks?kind=service-tiers", {signal}),
    enabled: enabled && !!owner, retry: false, staleTime: 15000,
    refetchInterval: q => !enabled ? false : q.state.data?.data.some(tierPending) ? 3000 : 60000,
  });
  const index = useMemo(() => tierIndex(query.data?.data || []), [query.data]);
  return {index, isError: !!enabled && query.isError, isPending: !!enabled && query.isPending};
}

export function useTierMembers(owner: string, enabled: boolean) {
  const query = useQuery({
    queryKey: ["service-tier-members", owner],
    queryFn: ({signal}) => controlRequest<{data: InstalledChannel[]; unavailable_sources: string[]}>(
      "/v1/channel-management?view=service-tiers", {signal}),
    enabled: enabled && !!owner, retry: false, staleTime: 30000,
    refetchInterval: enabled ? 60000 : false,
  });
  const members = useMemo(() => new Map((query.data?.data || []).map(member => [tierMemberKey(member), member])), [query.data]);
  return {members, isError: !!enabled && query.isError, isPending: !!enabled && query.isPending,
    unavailable: query.data?.unavailable_sources || []};
}

export function refreshTierViews(client: QueryClient, owner: string) {
  return Promise.all([
    client.invalidateQueries({queryKey: ["service-tier-table", owner]}, {cancelRefetch: false}),
    client.invalidateQueries({queryKey: ["service-tier-checks", owner]}, {cancelRefetch: false}),
  ]);
}

export function submitTierChecks(tier: ServiceTier, targets: TierTarget[], manual: boolean) {
  return controlRequest<{queued: number}>("/v1/channel-management/checks", {
    method: "POST", body: JSON.stringify({
      kind: tier, manual, confirmed: true, targets: targets.map(({model, scope, ...target}) =>
        ({...target, models: [model], expected_scope: scope})),
    }),
  }, {localConfiguredCheck: true});
}
