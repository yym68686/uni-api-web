import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel } from "./sub2apiImports";
import { modelChecks } from "./sub2apiResults";

export const UNASSIGNED_ACCOUNT = "__unassigned__";
export interface ManagedChannel extends InstalledChannel {
  engine: string;
  account_ids: string[];
  probe_fingerprint?: string;
  // Grouping is presentation-only. Mutations must use an individual member.
  members?: ManagedChannel[];
}
export interface ConfiguredCheck {
  source_id: string;
  provider: string;
  kind: "model" | "compaction" | "tool-use";
  model: string;
  fingerprint: string;
  state: string;
  message: string;
  result: NonNullable<SubTarget["result"]> | SubTarget["compaction"] | null;
  history: SubTarget["history"];
}
export function useConfiguredChecks() {
  return useQuery({
    queryKey: ["configured-checks"],
    queryFn: ({ signal }) => controlRequest<{ data: ConfiguredCheck[] }>("/v1/channel-management/checks", { signal }),
    refetchInterval: query => query.state.data?.data.some(c => ["queued", "running"].includes(c.state)) ? 1500 : 15000,
    retry: false,
  });
}
export const channelMembers = (item: ManagedChannel) => item.members || [item];
export function groupManagedChannels(
  channels: ManagedChannel[],
): ManagedChannel[] {
  const groups = new Map<string, ManagedChannel[]>();
  for (const item of channels) {
    // The same configured provider may use a public URL on one gateway and a
    // private URL on another. Group its presentation by configured name/engine;
    // keep addresses, credentials, model definitions and writes source-scoped.
    const id = JSON.stringify([item.provider, item.engine]);
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
  accountFilter = "",
  configuredChecks: ConfiguredCheck[] = [],
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
    const visibleBindings =
      accountFilter && accountFilter !== UNASSIGNED_ACCOUNT
        ? bindings.filter((b) => b.account?.id === accountFilter)
        : bindings;
    const preferred =
      visibleBindings.find(
        (b) => b.target && (!model || b.member.models.includes(model)),
      ) || visibleBindings.find((b) => b.target);
    const { account, target: bound } = preferred || {};
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
    // Results belong to a source/provider credential fingerprint. Never reuse
    // another source's result after its configured credential has changed.
    const native = configuredChecks.filter(c => members.some(m => m.source_id === c.source_id && m.provider === c.provider &&
      (!c.fingerprint || !m.probe_fingerprint || c.fingerprint === m.probe_fingerprint)));
    const latestCapability = (kind: ConfiguredCheck["kind"]) => native.filter(c => c.kind === kind).sort((a,b) =>
      Number(["queued","running"].includes(b.state)) - Number(["queued","running"].includes(a.state)) ||
      (b.result?.checked_at || 0) - (a.result?.checked_at || 0))[0];
    for (const [kind, field] of [["compaction","compaction"],["tool-use","tool_use"]] as const) {
      const c = latestCapability(kind);
      if (c && (!bound || (c.result?.checked_at || 0) >= (target[field]?.checked_at || 0))) {
        target[`${field}_state`] = c.state;
        target[field] = c.result as SubTarget[typeof field] || (c.message ? {status:"error",checked_at:0,message:c.message,attempts:[]} : undefined);
      }
    }
    const entry = row(
      displayAccount,
      target,
      `configured:${item.source_id}:${item.provider}`,
      item.account_ids || [],
      item,
    );
    entry.checks = item.models.map((name) => {
      const candidates = visibleBindings
        .filter((b) => b.member.models.includes(name))
        .flatMap((b) =>
          b.target
            ? modelChecks(b.target).filter(
                (c) => c.model === (b.member.model_mappings?.[name] || name),
              )
            : [],
        );
      for (const c of native.filter(c => c.kind === "model" && c.model === name)) {
        candidates.push({ model:name, source_name:members.find(m => m.source_id === c.source_id)?.source_name, state:c.state, message:c.message, result:c.result as SubTarget["result"] });
      }
      const check = candidates.sort(
        (a, b) => Number(["queued","running"].includes(b.state)) - Number(["queued","running"].includes(a.state)) || (b.result?.checked_at || 0) - (a.result?.checked_at || 0),
      )[0];
      return {
        ...(check || { state: "idle", message: "", result: null }),
        model: name,
      };
    });
    if (!bound) {
      target.models = entry.checks;
      const quality = native.filter(c => c.kind === "model" && c.model === "gpt-6-astra").sort((a,b) => (b.result?.checked_at || 0) - (a.result?.checked_at || 0))[0];
      if (quality) {
        target.history = quality.history;
        target.check_source_id = quality.source_id;
      }
    }
    entry.selected = entry.checks.find((c) => c.model === model);
    result.push(entry);
  }
  return result;
}
