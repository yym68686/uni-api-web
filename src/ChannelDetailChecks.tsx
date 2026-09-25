import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Play, RefreshCw } from "lucide-react";
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

type Kind = "availability" | "quality" | "tool-use" | "compaction";
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
    description: "自动选择本渠道模型验证",
  },
];
const pending = (state?: string) => state === "queued" || state === "running";
export function ChannelDetailChecks({ row }: { row: Channel }) {
  const client = useQueryClient(),
    query = useConfiguredChecks();
  const active = useRef(new Set<Kind>());
  const [submitting, setSubmitting] = useState(new Set<Kind>());
  const [errors, setErrors] = useState<Partial<Record<Kind, string>>>({});
  const [accepted, setAccepted] = useState<Partial<Record<Kind, boolean>>>({});
  const checks = (query.data?.data || []).filter(
    (c) => c.source_id === row.source_id && c.provider === row.provider,
  );
  const completed = checks
    .filter((c) => !pending(c.state))
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
  async function run(kind: Kind) {
    if (
      active.current.has(kind) ||
      pending(entry(kind)?.state) ||
      !row.source_id
    )
      return;
    active.current.add(kind);
    setSubmitting(new Set(active.current));
    setErrors((old) => ({ ...old, [kind]: undefined }));
    setAccepted((old) => ({ ...old, [kind]: false }));
    try {
      await controlRequest("/v1/channel-management/checks", {
        method: "POST",
        body: JSON.stringify({
          kind,
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
      await Promise.all([
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
      active.current.delete(kind);
      setSubmitting(new Set(active.current));
    }
  }
  const text = (kind: Kind, c?: ConfiguredCheck) => {
    if (!c) return "未检测";
    if (c.state === "queued") return "已排队";
    if (c.state === "running") return "检测中";
    if (c.state === "error") return "检测失败";
    if (c.state === "interrupted") return "已中断";
    if (kind === "availability")
      return importModelLabel({
        model: row.model,
        state: c.state,
        message: c.message,
        result: c.result as SubTarget["result"],
      });
    if (kind === "quality")
      return (
        (
          {
            pass: "不降智",
            fail: "降智",
            error: "检测失败",
            inconclusive: "无法判定",
          } as Record<string, string>
        )[(c.result as SubTarget["result"])?.verdict || ""] || "未检测"
      );
    const status = (c.result as ToolUseResult)?.status;
    return (kind === "tool-use" ? toolUseLabels : compactionLabels)[
      status || "untested"
    ];
  };
  return (
    <section className="detail-section detail-checks" aria-label="渠道独立检测">
      <h3>独立检测</h3>
      <p className="muted">
        {row.source_name || row.source_id} · {row.model}
        。仅检测当前渠道，不调整业务路由。
      </p>
      {query.isError && (
        <p role="alert">
          检测状态读取失败{" "}
          <button className="button small" onClick={() => void query.refetch()}>
            <RefreshCw size={13} />
            重试
          </button>
        </p>
      )}
      <div className="detail-check-grid">
        {items.map(({ kind, label, description }) => {
          const c = entry(kind),
            busy = submitting.has(kind) || pending(c?.state);
          const message =
            c?.message ||
            (kind === "availability"
              ? (c?.result as SubTarget["result"])?.availability?.message
              : kind === "quality"
                ? (c?.result as SubTarget["result"])?.quality?.message
                : (c?.result as ToolUseResult)?.message);
          const status = text(kind, c);
          return (
            <div key={kind} className="detail-check-item">
              <button
                className="button small"
                aria-label={label}
                aria-busy={busy}
                disabled={
                  busy || query.isPending || query.isError || !row.source_id
                }
                onClick={() => void run(kind)}
              >
                {busy ? <Spinner small /> : <Play size={13} />} {label}
              </button>
              <small>{description}</small>
              <span
                className={
                  status.includes("失败") ||
                  status.startsWith("不支持") ||
                  status === "降智"
                    ? "negative"
                    : "muted"
                }
                role="status"
              >
                {submitting.has(kind) ? "正在提交…" : status}
                {!busy && c?.result?.checked_at
                  ? ` · ${time(c.result.checked_at)}`
                  : ""}
              </span>
              {accepted[kind] && !c && (
                <small>已提交，正在读取检测状态。</small>
              )}
              {errors[kind] && (
                <p role="alert" className="negative">
                  {errors[kind]}
                </p>
              )}
              {message && <small title={message}>{message}</small>}
            </div>
          );
        })}
      </div>
    </section>
  );
}
