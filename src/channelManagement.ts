import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel } from "./sub2apiImports";
import { SUB_MODELS } from "./sub2apiModels";
import {toolUseModels} from "./toolUse";
import type {ToolUseResult,ToolUseModel} from "./toolUse";
import { modelChecks } from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { boundGroups } from "./sub2apiImports";

// Editing always uses the selected member's evidence, not another source's
// merged summary. Credentials changed since detection invalidate old checks.
export function configuredModelChecks(member:ManagedChannel, accounts:SubAccount[], checks:ConfiguredCheck[], models:string[]): SubModelCheck[] {
  const groups=boundGroups(member);
  const native=checks.filter(c=>(c.kind==="model"||c.kind==="availability") && c.source_id===member.source_id && c.provider===member.provider &&
    (c.fingerprint||"")===(member.probe_fingerprint||""));
  const available=(c:SubModelCheck)=>c.state==="done"&&c.result?.availability.status==="success";
  const recent=(items:SubModelCheck[])=>items.sort((a,b)=>
    Number(["queued","running"].includes(b.state))-Number(["queued","running"].includes(a.state)) ||
    (b.result?.checked_at||0)-(a.result?.checked_at||0) || Number(available(a))-Number(available(b)))[0];
  return models.map(model=>{
    const upstream=member.model_mappings?.[model]||model;
    const missing:SubModelCheck={model,state:"idle",message:"",result:null};
    const candidates:SubModelCheck[]=[];
    const site=groups.map(g=>{
      const target=accounts.find(a=>a.id===g.account_id)?.targets.find(t=>t.group_id===g.group_id);
      const check=target && modelChecks(target).find(c=>c.model===upstream);
      return check?{...check,model}:missing;
    });
    if(site.length){
      // A multi-key provider can reuse site evidence only when every bound
      // group passed. A single group's success cannot clear a sibling failure.
      const failed=site.filter(c=>!available(c));
      candidates.push(recent(failed.length?failed:site));
    }
    for(const check of native.filter(c=>(member.model_mappings?.[c.model]||c.model)===upstream))
      candidates.push({model,state:check.state,message:check.message,result:check.result as SubTarget["result"]});
    return recent(candidates) || missing;
  });
}

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
  kind: "model" | "availability" | "compaction" | "tool-use";
  model: string;
  fingerprint: string;
  state: string;
  message: string;
  result: NonNullable<SubTarget["result"]> | ToolUseResult | null;
  history: SubTarget["history"];
}

// Import defaults use the selected source and the exact public-to-upstream
// mapping, never the green summary of a different model/source.
export function configuredToolUseResult(member:ManagedChannel,accounts:SubAccount[],checks:ConfiguredCheck[]):ToolUseResult {
  const bound=accounts.find(a=>a.id===member.account_id)?.targets?.find(t=>t.group_id===member.group_id);
  const native=checks.filter(c=>c.kind==="tool-use" && c.source_id===member.source_id && c.provider===member.provider &&
    (!c.fingerprint || !member.probe_fingerprint || c.fingerprint===member.probe_fingerprint));
  const dispatcher=native.find(c=>!c.model);
  const legacy=toolUseModels(dispatcher?.result as ToolUseResult|undefined);
  const entries=new Map<string,ToolUseModel>();
  for(const name of member.models){
    const saved=toolUseModels(bound?.tool_use).find(m=>m.model===(member.model_mappings?.[name]||name));
    if(saved)entries.set(name,{...saved,model:name});
  }
  for(const entry of legacy)entries.set(entry.model,entry);
  for(const c of native.filter(c=>!!c.model)){
    const current=entries.get(c.model);
    if(!current?.result || (c.result?.checked_at||0)>=(current.result.checked_at||0)){
      entries.set(c.model,{model:c.model,state:c.state,result:c.result as ToolUseResult || (c.message ? {status:"error",model:c.model,message:c.message,checked_at:0,attempts:[]} : undefined)});
    }
  }
  return {status:"error",checked_at:Math.max(0,...[...entries.values()].map(m=>m.result?.checked_at||0)),attempts:[],models:[...entries.values()]};
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
export function useChannelManagement(enabled = true) {
  return useQuery({
    queryKey: ["channel-management"],
    queryFn: ({ signal }) =>
      controlRequest<{ data: ManagedChannel[]; unavailable_sources: string[] }>(
        "/v1/channel-management",
        { signal },
      ),
    enabled,
    staleTime: 15000,
    refetchInterval: enabled ? 30000 : false,
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
    for (const [kind, field] of [["compaction","compaction"]] as const) {
      const c = latestCapability(kind);
      if (c && (!bound || (c.result?.checked_at || 0) >= (target[field]?.checked_at || 0))) {
        target[`${field}_state`] = c.state;
        target[field] = c.result as SubTarget[typeof field] || (c.message ? {status:"error",checked_at:0,message:c.message,attempts:[]} : undefined);
      }
    }
    const toolEntries=new Map<string,ToolUseModel>();
    for(const member of members){
      for(const c of configuredToolUseResult(member,accounts,configuredChecks).models||[]){
        const previous=toolEntries.get(c.model);
        if(!previous || (c.result?.checked_at||0)>(previous.result?.checked_at||0))toolEntries.set(c.model,c);
      }
    }
    target.tool_use={status:"error",checked_at:Math.max(0,...[...toolEntries.values()].map(m=>m.result?.checked_at||0)),models:[...toolEntries.values()],attempts:[]};
    const toolPending=native.some(c=>c.kind==="tool-use" && ["queued","running"].includes(c.state));
    if(toolPending)target.tool_use_state="running";
    else if(!bound)target.tool_use_state="done";
    const entry = row(
      displayAccount,
      target,
      `configured:${item.source_id}:${item.provider}`,
      item.account_ids || [],
      item,
    );
    const checkModels = [...new Set([...item.models, ...(!bound ? SUB_MODELS : []), ...native.filter(c => (c.kind === "model" || c.kind === "availability")).map(c => c.model)])];
    entry.checks = checkModels.map((name) => {
      const candidates = visibleBindings
        .filter((b) => b.member.models.includes(name))
        .flatMap((b) =>
          b.target
            ? modelChecks(b.target).filter(
                (c) => c.model === (b.member.model_mappings?.[name] || name),
              )
            : [],
        );
      for (const c of native.filter(c => (c.kind === "model" || c.kind === "availability") && c.model === name)) {
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
        const result=quality.result as SubTarget["result"];
        if(result)target.quality_check={source_id:quality.source_id,provider:quality.provider,model:"gpt-6-astra",checked_at:result.checked_at,verdict:result.verdict as NonNullable<SubTarget["quality_check"]>["verdict"],text:result.quality.text,message:result.quality.message,duration_ms:result.quality.duration_ms,quality_probe:result.quality};
      }
    }
    entry.selected = entry.checks.find((c) => c.model === model);
    result.push(entry);
  }
  return result;
}
