import { FingerprintPanel } from "./ModelFingerprints";
import { isCapabilityCheckModel, capabilityCheckScope } from "./sub2apiModels";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, RefreshCw } from "./icons";
import { controlRequest } from "./api";
import { useConfiguredChecks } from "./channelManagement";
import type { ConfiguredCheck } from "./channelManagement";
import type { Channel } from "./types";
import type { SubTarget } from "./Sub2apiChecks";
import { importModelLabel } from "./sub2apiResults";
import type { ToolUseResult } from "./toolUse";
import { toolUseLabels } from "./toolUse";
import { compactionLabels } from "./SubCompaction";
import { Spinner } from "./ui";
import { time } from "./format";
import { WorkbenchNotice } from "./Workbench";
import { detectionProjectLabels } from "./DetectionAction";
import { isServiceTier, serviceTierFilterLabels, serviceTierFresh, serviceTierVerified, serviceTiers, type ServiceTier, type ServiceTierResult } from "./serviceTierStatus";
import { refreshTierViews, submitTierChecks } from "./serviceTierTable";
import type { ChannelCheck } from "./ChannelChecks";
import { QualityProbability } from "./QualityHistory";

type Kind = "availability" | "quality" | "tool-use" | "compaction" | ServiceTier;
const items: { kind: Kind; label: string; description: string }[] = [
  {
    kind: "availability",
    label: "可用性检测",
    description: "当前模型 · 完整流式响应",
  },
  {
    kind: "tool-use",
    label: "Tool use 检测",
    description: "当前模型 · custom exec 调用",
  },
  {
    kind: "quality",
    label: "降智检测",
    description: "gpt-6-astra · 现有降智探针",
  },
  {
    kind: "compaction",
    label: "远程压缩检测",
    description: "自动选择本渠道 gpt / codex 模型验证",
  },
  ...serviceTiers.map(kind => ({ kind, label: detectionProjectLabels[kind], description: "当前渠道 · 当前模型" })),
];
const pending = (state?: string) => state === "queued" || state === "running";
const qualityLabels = { pass: "不降智", fail: "降智", error: "检测失败", inconclusive: "无法判定" };
export function ChannelDetailChecks({ row, owner, quality }: { row: Channel; owner: string; quality?: ChannelCheck }) {
  const client = useQueryClient(),
    query = useConfiguredChecks();
  const mayProbeTier = !!row.source_id;
  const target = useQuery({
    queryKey: ["detail-tier-target", owner, row.source_id, row.provider, row.model],
    enabled: mayProbeTier,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    queryFn: ({signal}) => controlRequest<{
      applicable: boolean; scope: string;
      group_results?: Partial<Record<ServiceTier, ServiceTierResult>>;
    }>(`/v1/sources/${encodeURIComponent(row.source_id!)}/service-tier-target?` +
      new URLSearchParams({provider: row.provider, model: row.model}), {signal}),
  });
  const tierReadFailed = mayProbeTier && target.isError;
  const tierTarget = mayProbeTier && target.data?.applicable && target.data.scope && row.source_id
    ? { source_id: row.source_id, provider: row.provider, model: row.model, scope: target.data.scope } : undefined;
  const active = useRef(new Set<Kind>());
  const [submitting, setSubmitting] = useState(new Set<Kind>());
  const [errors, setErrors] = useState<Partial<Record<Kind, string>>>({});
  const [accepted, setAccepted] = useState<Partial<Record<Kind, boolean>>>({});
  const checks = (query.data?.data || []).filter(
    (c) => c.source_id === row.source_id && c.provider === row.provider,
  );
  const completed = checks
    .filter((c) => !isServiceTier(c.kind) && !pending(c.state))
    .map((c) => `${c.kind}:${c.model}:${c.state}:${c.result?.checked_at}`)
    .sort()
    .join("|");
  useEffect(() => {
    if (!completed) return;
    for (const key of [
      "channel-checks",
      "quality-history",
      "sub-quality-summary",
    ])
      void client.invalidateQueries({ queryKey: [key] });
  }, [client, completed]);
  const tierCompleted = checks.filter(c => isServiceTier(c.kind) && c.model === row.model && !pending(c.state))
    .map(c => `${c.kind}:${c.state}:${c.result?.checked_at}`).sort().join("|");
  useEffect(() => {
    if (tierCompleted) void refreshTierViews(client, owner);
  }, [client, owner, tierCompleted]);
  function entry(kind: Kind): ConfiguredCheck | undefined {
    const model =
      kind === "quality"
        ? "gpt-6-astra"
        : kind === "compaction"
          ? ""
          : row.model;
    const exact = checks.find(
      (c) =>
        c.kind === (kind === "quality" ? "model" : kind) && c.model === model,
    );
    if (kind !== "availability") return exact;
    const combined = checks.find(
      (c) => c.kind === "model" && c.model === model,
    );
    // Show ordinary model-check evidence until a newer standalone test exists.
    if (
      !exact ||
      (!pending(exact.state) &&
        (combined?.result?.checked_at || 0) > (exact.result?.checked_at || 0))
    )
      // A separate quality job must not lock the availability button. Its
      // previous completed response can still provide availability evidence.
      return combined && pending(combined.state)
        ? { ...combined, state: combined.result ? "done" : "idle" }
        : combined;
    return exact;
  }
  const applicable = (kind: Kind) =>
    isServiceTier(kind) ? !!tierTarget :
      (kind !== "compaction" && kind !== "tool-use") || isCapabilityCheckModel(row.model);
  const tierLoading = mayProbeTier && (target.isPending || query.isPending);
  const tierBlocked = tierReadFailed || query.isError || tierLoading;
  async function run(kind: Kind) {
    if (
      !applicable(kind) ||
      active.current.has(kind) ||
      pending(entry(kind)?.state) ||
      !row.source_id
    )
      return;
    if (isServiceTier(kind) && (!tierTarget || tierBlocked)) return;
    active.current.add(kind);
    setSubmitting(new Set(active.current));
    setErrors((old) => ({ ...old, [kind]: undefined }));
    setAccepted((old) => ({ ...old, [kind]: false }));
    try {
      if (isServiceTier(kind) && tierTarget) {
        await submitTierChecks(kind, [tierTarget], true);
      } else await controlRequest("/v1/channel-management/checks", {
        method: "POST",
        body: JSON.stringify({
          kind,
          manual: true,
          targets: [
            {
              source_id: row.source_id,
              provider: row.provider,
              models:
                kind === "compaction"
                  ? []
                  : [kind === "quality" ? "gpt-6-astra" : row.model],
            },
          ],
        }),
      });
      setAccepted((old) => ({ ...old, [kind]: true }));
      if (!isServiceTier(kind)) await Promise.all([
        client.invalidateQueries({ queryKey: ["configured-checks"] }),
        client.invalidateQueries({ queryKey: ["channel-checks"] }),
        client.invalidateQueries({ queryKey: ["sub2api"] }),
      ]);
    } catch (e) {
      setErrors((old) => ({
        ...old,
        [kind]: e instanceof Error ? e.message : "检测提交失败",
      }));
    } finally {
      if (isServiceTier(kind)) {
        // A lost receipt can still mean the job was queued. Refresh without replaying it.
        void refreshTierViews(client, owner);
        await client.invalidateQueries({ queryKey: ["configured-checks"] }, { cancelRefetch: false });
      }
      active.current.delete(kind);
      setSubmitting(new Set(active.current));
    }
  }
  const tierResult = (c?: ConfiguredCheck) =>
    c && tierTarget && c.fingerprint === tierTarget.scope ? c.result as ServiceTierResult | null : null;
  const displayedTier = (kind: ServiceTier, c?: ConfiguredCheck) => {
    const own = tierResult(c);
    const shared = target.data?.group_results?.[kind];
    return shared && serviceTierFresh(shared.checked_at) &&
      (!own || !serviceTierFresh(own.checked_at) || shared.checked_at > own.checked_at)
      ? {result: shared, shared: true} : {result: own, shared: false};
  };
  const text = (kind: Kind, c: ConfiguredCheck | undefined, tier: ReturnType<typeof displayedTier> | null, latestQuality?: ChannelCheck) => {
    if (isServiceTier(kind)) {
      if (tierReadFailed || query.isError) return "读取失败";
      if (tierLoading) return "读取中…";
    }
    if (!applicable(kind)) return "不适用";
    if (!c && !tier?.result && !latestQuality) return "未检测";
    if (c?.state === "queued") return "已排队";
    if (c?.state === "running") return "检测中";
    if (tier) {
      const {result, shared} = tier;
      if (!result || !serviceTierFresh(result.checked_at)) return c?.state === "error" ? "检测失败" : c?.state === "interrupted" ? "已中断" : "未确认";
      if (result.status === "not_applicable") return "不适用";
      if (result.status === "inconclusive") return "结果未确认";
      if (result.status === "supported" && !shared && !serviceTierVerified(result)) return "未确认";
      return serviceTierFilterLabels[result.status] || "未确认";
    }
    if (latestQuality) return qualityLabels[latestQuality.verdict];
    if (!c || c.state === "error") return "检测失败";
    if (kind === "availability")
      return importModelLabel({
        model: row.model,
        state: c.state,
        message: c.message,
        result: c.result as SubTarget["result"],
      });
    if (c.state === "interrupted") return "已中断";
    if (kind === "quality") {
      const verdict = (c.result as SubTarget["result"])?.verdict;
      return verdict && Object.hasOwn(qualityLabels, verdict)
        ? qualityLabels[verdict as keyof typeof qualityLabels] : "未检测";
    }
    const status = (c.result as ToolUseResult)?.status;
    return (kind === "tool-use" ? toolUseLabels : compactionLabels)[
      status || "untested"
    ];
  };
  return (
    <section className="detail-section detail-checks" aria-label="渠道独立检测">
      <div className="detail-section-heading">
        <h3>独立检测</h3>
        <button className="button small detail-check-retry" hidden={!query.isError && !tierReadFailed}
          aria-label="检测状态读取失败 · 重试" title="检测状态读取失败 · 重试"
          onClick={() => { void query.refetch(); if (mayProbeTier) void target.refetch(); }}>
          <RefreshCw size={18} />
        </button>
      </div>
      <div className="detail-check-grid">
        {items.map(({ kind, label, description }) => {
          const c = applicable(kind) ? entry(kind) : undefined,
            busy = submitting.has(kind) || pending(c?.state);
          const latestQuality = kind === "quality" && quality &&
            (!c || quality.checked_at > (c.result?.checked_at || 0)) ? quality : undefined;
          const message =
            latestQuality?.message || c?.message ||
            (kind === "availability"
              ? (c?.result as SubTarget["result"])?.availability?.message
              : kind === "quality"
                ? (c?.result as SubTarget["result"])?.quality?.message
                : !isServiceTier(kind) ? (c?.result as ToolUseResult)?.message : undefined);
          const tier = isServiceTier(kind) ? displayedTier(kind, c) : null;
          const status = text(kind, c, tier, latestQuality);
          const tierEvidence = tier?.result;
          const observed = tierEvidence?.observed;
          const checkedAt = tier ? tierEvidence?.checked_at : latestQuality?.checked_at || c?.result?.checked_at;
          return (
            <div key={kind} className="detail-check-item">
              <button
                className="button small"
                aria-label={label}
                title={applicable(kind) || isServiceTier(kind) ? description : capabilityCheckScope}
                aria-busy={busy}
                disabled={!applicable(kind) || busy || !row.source_id || isServiceTier(kind) && tierBlocked}
                onClick={() => void run(kind)}
              >
                {busy ? <Spinner small /> : <Play size={13} />} {label}
              </button>
              <span
                data-quality={kind === "quality" || undefined}
                className={
                  status === "已验证支持" || status === "不降智" ? "positive" :
                    status === "未命中" || status === "结果未确认" || status === "已中断" ? "detail-check-warning" :
                  status.includes("失败") || status === "明确不支持" ||
                  status.startsWith("不支持") ||
                  status === "降智"
                    ? "negative"
                    : "muted"
                }
                role="status"
                title={[tier?.shared && "分组检测", observed && `实际档位：${observed}`, checkedAt && time(checkedAt)].filter(Boolean).join(" · ") || undefined}
              >
                {submitting.has(kind) ? "正在提交…" : accepted[kind] && !c && !tierEvidence ? "已提交" : status}
                {kind === "quality" && quality && !busy && <QualityProbability history={quality.history} checkedAt={quality.checked_at} origin={quality.origin} />}
              </span>
              <WorkbenchNotice urgent={!!errors[kind]} label={`${label}详情`} messages={[errors[kind], message]} />
            </div>
          );
        })}
        {row.source_id && <FingerprintPanel inline targets={[{source_id:row.source_id,provider:row.provider,model:row.model}]}/>}
      </div>
    </section>
  );
}
