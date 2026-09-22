import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel } from "./sub2apiImports";
import { modelChecks } from "./sub2apiResults";

export const UNASSIGNED_ACCOUNT = "__unassigned__";
export interface ManagedChannel extends InstalledChannel {
  engine: string;
  account_ids: string[];
  // Grouping is presentation-only. Mutations must use an individual member.
  members?: ManagedChannel[];
}
export const channelMembers = (item: ManagedChannel) => item.members || [item];
export function groupManagedChannels(
  channels: ManagedChannel[],
): ManagedChannel[] {
  const groups = new Map<string, ManagedChannel[]>();
  for (const item of channels) {
    // The API normalizes known inference URL suffixes. Never combine unrelated
    // upstreams or unknown addresses merely because their display names match.
    const id = JSON.stringify(
      item.base
        ? [item.provider, item.base.replace(/\/+$/, ""), item.engine]
        : [item.provider, null, item.source_id],
    );
    const members = groups.get(id) || [];
    members.push(item);
    groups.set(id, members);
  }
  return [...groups.values()].map((members) => ({
    ...members[0],
    members,
    source_name: [...new Set(members.map((m) => m.source_name))].join(" / "),
    models: [...new Set(members.flatMap((m) => m.models))],
    account_ids: [...new Set(members.flatMap((m) => m.account_ids || []))],
  }));
}
export function useChannelManagement() {
  return useQuery({
    queryKey: ["channel-management"],
    queryFn: ({ signal }) =>
      controlRequest<{ data: ManagedChannel[]; unavailable_sources: string[] }>(
        "/v1/channel-management",
        { signal },
      ),
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
export function managementRows(
  accounts: SubAccount[],
  channels: ManagedChannel[],
  installed: InstalledChannel[],
  model: string,
): ManagementRow[] {
  const row = (
    account: SubAccount,
    target: SubTarget,
    id: string,
    accountIds: string[],
    configured?: ManagedChannel,
  ): ManagementRow => {
    const saved = modelChecks(target);
    const checks = configured
      ? configured.models.map((model) => ({
          ...(saved.find(
            (c) => c.model === (configured.model_mappings?.[model] || model),
          ) || { state: "idle", message: "", result: null }),
          model,
        }))
      : saved;
    return {
      id,
      account,
      target,
      accountIds,
      configured,
      checks,
      selected: checks.find((c) => c.model === model),
    };
  };
  const result = accounts.flatMap((account) =>
    account.targets.map((target) =>
      row(account, target, `${account.id}:${target.group_id}`, [account.id]),
    ),
  );
  const imported = new Set(
    installed
      .filter((i) => i.kind !== "configured")
      .map((i) => `${i.source_id}:${i.provider}`),
  );
  for (const item of groupManagedChannels(
    channels.filter(
      (item) =>
        item.provider && !imported.has(`${item.source_id}:${item.provider}`),
    ),
  )) {
    const members = channelMembers(item);
    const bindings = members.map((member) => {
      const account = accounts.find((a) => a.id === member.account_id);
      return {
        member,
        account,
        target: account?.targets.find((t) => t.group_id === member.group_id),
      };
    });
    const preferred =
      bindings.find(
        (b) => b.target && (!model || b.member.models.includes(model)),
      ) ||
      bindings.find((b) => b.target) ||
      bindings[0];
    const { account, target: bound } = preferred;
    const displayAccount = account || {
      id: "",
      name:
        item.account_ids
          ?.map((id) => accounts.find((a) => a.id === id)?.name)
          .filter(Boolean)
          .join("、") || "未归属渠道",
      base: item.base || "",
      email: "",
      state: "idle",
      message: "",
      synced_at: 0,
      targets: [],
    };
    const target: SubTarget = bound
      ? { ...bound, name: item.name || item.provider, channel: item.provider }
      : {
          group_id: 0,
          name: item.name || item.provider,
          channel: item.provider,
          platform: ["gpt", "codex"].includes(item.engine)
            ? "openai"
            : item.engine,
          rate: 0,
          key_id: 0,
          active: false,
          state: "idle",
          message: "",
          result: null,
        };
    const entry = row(
      displayAccount,
      target,
      `configured:${item.source_id}:${item.provider}`,
      item.account_ids || [],
      item,
    );
    entry.checks = item.models.map((name) => {
      const candidates = bindings
        .filter((b) => b.member.models.includes(name))
        .flatMap((b) =>
          b.target
            ? modelChecks(b.target).filter(
                (c) => c.model === (b.member.model_mappings?.[name] || name),
              )
            : [],
        );
      const check = candidates.sort(
        (a, b) => (b.result?.checked_at || 0) - (a.result?.checked_at || 0),
      )[0];
      return {
        ...(check || { state: "idle", message: "", result: null }),
        model: name,
      };
    });
    entry.selected = entry.checks.find((c) => c.model === model);
    result.push(entry);
  }
  return result;
}
