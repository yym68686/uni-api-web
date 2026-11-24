import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown } from "lucide-react";
import { controlRequest } from "./api";
import { submitTierChecks, tierTarget } from "./serviceTierTable";
import type { ConfiguredCheck } from "./channelManagement";
import type { InstalledChannel } from "./sub2apiImports";
import { serviceTierFilterLabels, serviceTierFresh, serviceTierName, serviceTiers, serviceTierVerified } from "./serviceTierStatus";
import type { ServiceTier, ServiceTierResult } from "./serviceTierStatus";
import "./serviceTierChecks.css";

const memberKey = (member: InstalledChannel) => JSON.stringify([member.source_id, member.provider]);
const pending = (check?: ConfiguredCheck) => check?.state === "queued" || check?.state === "running";
const date = (value: number) => new Date(value * 1000).toLocaleString();

export function ServiceTierChecks({ owner, members, initialModel }: {
  owner: string;
  members: InstalledChannel[];
  initialModel?: string;
}) {
  const choices = [...new Map(members.filter(member => member.service_tier_scope &&
    member.models.length && (member.engine === "gpt" || member.engine === "codex"))
    .map(member => [memberKey(member), member])).values()];
  const [selection, setSelection] = useState("");
  const current = choices.find(member => memberKey(member) === selection)
    || choices.find(member => initialModel && member.models.includes(initialModel)) || choices[0];
  return current && <section className="detail-section" aria-label="服务档位">
    <h3>Fast / Ultrafast</h3>
    {choices.length > 1 && <label className="select-field">
      <select aria-label="档位检测渠道" value={memberKey(current)} onChange={event => setSelection(event.target.value)}>
        {choices.map(member => <option key={memberKey(member)} value={memberKey(member)}>
          {member.source_name || member.source_id} · {member.provider}
        </option>)}
      </select><ChevronDown size={13} />
    </label>}
    <TierMember key={memberKey(current) + current.service_tier_scope} owner={owner} member={current} initialModel={initialModel} />
  </section>;
}

function TierMember({ owner, member, initialModel }: { owner: string; member: InstalledChannel; initialModel?: string }) {
  const [model, setModel] = useState(member.models.find(name => name === initialModel || member.model_mappings?.[name] === initialModel) || member.models[0]);
  return <div className="service-tier-controls">
    <label className="select-field">
      <select aria-label="档位检测模型" value={model} onChange={event => setModel(event.target.value)}>
        {member.models.map(name => <option key={name} value={name}>{name}</option>)}
      </select><ChevronDown size={13} />
    </label>
    <TierModel key={model} owner={owner} member={member} model={model} />
  </div>;
}

function TierModel({ owner, member, model }: { owner: string; member: InstalledChannel; model: string }) {
  const client = useQueryClient();
  const queryKey = ["service-tier-checks", owner, member.source_id, member.provider, member.service_tier_scope, model];
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => controlRequest<{data: ConfiguredCheck[]}>(
      "/v1/channel-management/checks?" + new URLSearchParams({ source_id: member.source_id, provider: member.provider, model }),
      { signal },
    ),
    retry: false,
    refetchInterval: q => q.state.data?.data.some(check =>
      check.model === model && serviceTiers.some(tier => tier === check.kind) && pending(check)) ? 2000 : false,
  });
  const wasPending = useRef<ServiceTier[]>([]);
  const refreshManagement = () => void client.invalidateQueries({
    queryKey: ["management-query", owner],
    predicate: q => {
      const filter = q.queryKey[2] as { fast?: string; ultrafast?: string } | undefined;
      return !!(filter?.fast || filter?.ultrafast);
    },
  }, { cancelRefetch: false });
  useEffect(() => {
    if (!query.data) return;
    const current = serviceTiers.filter(tier =>
      query.data.data.some(check => check.kind === tier && check.model === model && pending(check)));
    if (wasPending.current.some(tier => !current.includes(tier))) refreshManagement();
    wasPending.current = current;
  }, [query.data, model]);

  return <>
    {query.isError && <div role="alert" className="negative">{query.error.message} <button type="button" className="button small" onClick={() => void query.refetch()}>重试读取</button></div>}
    {serviceTiers.map(tier => {
      const check = query.data?.data.find(check => check.kind === tier && check.model === model);
      return <TierAction key={tier} tier={tier} member={member} model={model} check={check}
        ready={query.isSuccess} reload={() => query.refetch({cancelRefetch: false})} refreshManagement={refreshManagement} />;
    })}
  </>;
}

function TierAction({ tier, member, model, check, ready, reload, refreshManagement }: {
  tier: ServiceTier; member: InstalledChannel; model: string; check?: ConfiguredCheck;
  ready: boolean; reload: () => Promise<unknown>; refreshManagement: () => void;
}) {
  const busy = useRef(false);
  const [working, setWorking] = useState(false);
  const [notice, setNotice] = useState<{error?: boolean; text: string}>();
  const result = check?.fingerprint === member.service_tier_scope ? check?.result as ServiceTierResult | undefined : undefined;
  const verified = serviceTierVerified(result);
  const active = working || pending(check);
  const label = serviceTierName[tier];
  const status = pending(check) ? check?.state === "queued" ? "排队中" : "检测中" :
    check?.state === "error" || check?.state === "interrupted" ? "检测未确认" :
    !result || !serviceTierFresh(result.checked_at) ? "未确认" :
    result.status === "not_applicable" ? "不适用" :
    result.status === "inconclusive" ? "检测未确认" : serviceTierFilterLabels[result.status];
  const description = [status, result?.observed && `实际 ${result.observed}`,
    result?.checked_at && date(result.checked_at),
    verified && result?.status !== "supported" && result?.verified_at && `上次验证 ${date(result.verified_at)}`,
    check?.message].filter(Boolean).join(" · ");
  async function act(action: "probe" | "publish" | "remove") {
    if (busy.current || pending(check)) return;
    const confirm = action === "probe" ? `检测 ${member.provider} / ${model} 的 ${label} 将发送一次可能计费的上游请求。继续？` :
      action === "publish" ? `将 ${member.provider} / ${model} 的 ${label} 证据应用到路由？` :
        `撤销 ${member.provider} / ${model} 的 ${label} 路由证据？`;
    if (!window.confirm(confirm)) return;
    busy.current = true;
    setWorking(true);
    setNotice(undefined);
    try {
      if (action === "probe") {
        const target = tierTarget(member, model);
        if (!target) throw Error("当前模型不适用档位检测");
        await submitTierChecks(tier, [target], true);
      } else {
        const receipt = await controlRequest<{status?: string; message?: string}>(`/v1/sources/${encodeURIComponent(member.source_id)}/service-tier-evidence`, {
          method: "POST", body: JSON.stringify({ provider: member.provider, model, tier, operation_id: crypto.randomUUID(), remove: action === "remove" }),
        });
        if (receipt.status !== "applied" && receipt.status !== "applied_unretained") {
          throw Error(receipt.message || "路由证据变更未确认");
        }
        setNotice({ text: receipt.message || (action === "remove" ? "已撤销路由证据" : "已应用到路由") });
      }
    } catch (error) {
      setNotice({ error: true, text: error instanceof Error ? error.message : "操作未确认" });
    } finally {
      await reload();
      refreshManagement();
      busy.current = false;
      setWorking(false);
    }
  }
  return <div className="service-tier-row">
    <strong>{label}</strong>
    <span className={status === "已验证支持" || verified ? "positive" : status === "明确不支持" ? "negative" : "muted"}
      title={description}>{description}</span>
    <div className="service-tier-actions">
      <button type="button" className="button small" disabled={!ready || active} aria-label={`检测 ${label}`} aria-busy={active}
        onClick={() => void act("probe")}>检测</button>
      <button type="button" className="button small" disabled={!verified || active} aria-label={`应用 ${label} 到路由`}
        onClick={() => void act("publish")}>应用到路由</button>
      {result?.checked_at && <button type="button" className="button small" disabled={active} aria-label={`撤销 ${label} 路由证据`}
        onClick={() => void act("remove")}>撤销</button>}
    </div>
    {notice && <small role={notice.error ? "alert" : "status"} className={notice.error ? "negative" : "positive"}>{notice.text}</small>}
  </div>;
}
