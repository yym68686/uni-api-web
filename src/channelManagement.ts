import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel } from "./sub2apiImports";
import { modelChecks } from "./sub2apiResults";

export const UNASSIGNED_ACCOUNT = "__unassigned__";
export interface ManagedChannel extends InstalledChannel {
  engine: string;
  account_ids: string[];
}
export function useChannelManagement() {
  return useQuery({
    queryKey: ["channel-management"],
    queryFn: ({ signal }) => controlRequest<{ data: ManagedChannel[]; unavailable_sources: string[] }>("/v1/channel-management", { signal }),
    staleTime: 15000,
    refetchInterval: 30000,
    retry: false,
  });
}
export interface ManagementRow {
  id: string;
  account: SubAccount;
  target: SubTarget;
  accountIds: string[];
  configured?: ManagedChannel;
  checks: ReturnType<typeof modelChecks>;
  selected: ReturnType<typeof modelChecks>[number] | undefined;
}
export function managementRows(accounts: SubAccount[], channels: ManagedChannel[], installed: InstalledChannel[], model: string): ManagementRow[] {
  const row = (account: SubAccount, target: SubTarget, id: string, accountIds: string[], configured?: ManagedChannel): ManagementRow => {
    const saved = modelChecks(target);
    const checks = configured ? configured.models.map(model => saved.find(c => c.model === model) || { model, state: "idle", message: "", result: null }) : saved;
    return { id, account, target, accountIds, configured, checks, selected: checks.find(c => c.model === model) };
  };
  const result = accounts.flatMap(account => account.targets.map(target => row(account, target, `${account.id}:${target.group_id}`, [account.id])));
  const imported = new Set(installed.filter(i => i.kind !== "configured").map(i => `${i.source_id}:${i.provider}`));
  for (const item of channels) {
    if (!item.provider || imported.has(`${item.source_id}:${item.provider}`)) continue;
    const account = accounts.find(a => a.id === item.account_id);
    const bound = account?.targets.find(t => t.group_id === item.group_id);
    const displayAccount = account || {
      id: "", name: item.account_ids?.map(id => accounts.find(a => a.id === id)?.name).filter(Boolean).join("、") || "未归属渠道",
      base: item.base || "", email: "", state: "idle", message: "", synced_at: 0, targets: [],
    };
    const target: SubTarget = bound ? { ...bound, name: item.name || item.provider, channel: item.provider } : {
      group_id: 0, name: item.name || item.provider, channel: item.provider,
      platform: ["gpt", "codex"].includes(item.engine) ? "openai" : item.engine, rate: 0, key_id: 0, active: false, state: "idle", message: "", result: null,
    };
    result.push(row(displayAccount, target, `configured:${item.source_id}:${item.provider}`, item.account_ids || [], item));
  }
  return result;
}
