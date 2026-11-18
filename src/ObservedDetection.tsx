import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Play, ScanLine, X } from "lucide-react";
import { controlRequest } from "./api";
import { checkTargets, type Checks } from "./ChannelChecks";
import { detectionKey, useChannelDetection } from "./channelDetection";
import { useConfiguredChecks } from "./channelManagement";
import {
  DetectionAction,
  detectionProjectLabels,
  detectionProjects,
  useDetectionProject,
} from "./DetectionAction";
import { fingerprintKey } from "./fingerprintResults";
import { channelName, providerId } from "./format";
import {
  fingerprintBusy,
  FINGERPRINT_BATCH_LIMIT,
  type useModelFingerprints,
} from "./ModelFingerprints";
import { compactionLabels } from "./SubCompaction";
import { capabilityCheckScope, isCapabilityCheckModel } from "./sub2apiModels";
import { toolUseLabels } from "./toolUse";
import type { Channel } from "./types";
import { Spinner } from "./ui";

type Target = { source_id: string; provider: string; model: string };
type Fingerprints = Pick<
  ReturnType<typeof useModelFingerprints>,
  "index" | "members" | "supportedModels" | "isPending" | "isError"
>;
const pending = (state?: string) => state === "queued" || state === "running";

export function useObservedDetection(
  owner: string,
  visible: boolean,
  rows: Channel[],
  checks: Checks,
  fingerprints: Fingerprints,
) {
  const [project, selectProject] = useDetectionProject(
    "observation",
    owner,
    detectionProjects,
  );
  const [expanded, setExpanded] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean }>();
  const [submitting, setSubmitting] = useState<Set<string>>(new Set());
  const inFlight = useRef(new Set<string>());
  const batchInFlight = useRef(false);
  const [batchActive, setBatchActive] = useState(false);
  const client = useQueryClient();
  const additional = visible && expanded && project !== "quality";
  const detection = useChannelDetection(owner, additional);
  const capability = project === "compaction" || project === "tool-use";
  const configured = useConfiguredChecks(additional && capability);
  const configuredIndex = useMemo(
    () =>
      new Map(
        (configured.data?.data || []).map((check) => [
          `${check.kind}:${fingerprintKey(check)}`,
          check,
        ]),
      ),
    [configured.data],
  );
  const qualityRows = project === "quality" ? checkTargets(rows) : [];

  function targetFor(row: Channel): Target | undefined {
    if (!row.source_id) return;
    if (project === "fingerprint") {
      if (
        fingerprints.isPending ||
        fingerprints.isError ||
        !fingerprints.supportedModels.includes(row.model)
      )
        return;
      const member = fingerprints.members.get(providerId(row));
      if (
        !member?.models.includes(row.model) ||
        (member.model_mappings?.[row.model] || row.model) !== row.upstream_model
      )
        return;
    } else if (!isCapabilityCheckModel(row.model)) return;
    // The existing compaction probe automatically picks a suitable model per channel.
    return {
      source_id: row.source_id,
      provider: row.provider,
      model: project === "compaction" ? "" : row.model,
    };
  }
  const entry = (target: Target) =>
    configuredIndex.get(`${project}:${fingerprintKey(target)}`);
  const submissionKey = (target: Target) => JSON.stringify([owner,project,fingerprintKey(target)]);
  const busy = (target: Target) =>
    submitting.has(submissionKey(target)) ||
    (project === "fingerprint"
      ? fingerprintBusy(fingerprints.index.get(fingerprintKey(target)))
      : pending(entry(target)?.state));
  const batchTargets = new Map<string, Target>();
  if (additional && !detection.isPending && !detection.isError) {
    for (const row of rows) {
      const target = targetFor(row);
      if (
        target &&
        !busy(target) &&
        !detection.disabled.has(detectionKey(target))
      )
        batchTargets.set(fingerprintKey(target), target);
    }
  }
  const total = project === "quality" ? qualityRows.length : batchTargets.size;
  const batchIssue = !additional
    ? ""
    : detection.isError
      ? "检测开关读取失败，无法批量检测。"
      : detection.isPending
        ? "正在读取批量检测设置…"
        : project === "fingerprint" &&
            (fingerprints.isPending || fingerprints.isError)
          ? "指纹检测支持信息尚不可用。"
          : project === "fingerprint" && total > FINGERPRINT_BATCH_LIMIT
            ? `单次最多提交 ${FINGERPRINT_BATCH_LIMIT} 项指纹检测，请缩小筛选范围。`
            : !total
              ? "当前筛选没有可发起此项检测的渠道。"
              : "";

  async function queue(targets: Target[], manual = false) {
    if (project === "quality" || !manual && batchInFlight.current) return;
    targets = targets.filter(target => !inFlight.current.has(submissionKey(target)) && !busy(target));
    if (!targets.length) return;
    const keys = targets.map(submissionKey);
    keys.forEach(key => inFlight.current.add(key));
    setSubmitting(new Set(inFlight.current));
    if (!manual) { batchInFlight.current = true; setBatchActive(true); }
    setNotice(undefined);
    try {
      const fingerprint = project === "fingerprint";
      const result = await controlRequest<{ queued?: number }>(
        fingerprint
          ? "/v1/model-fingerprints"
          : "/v1/channel-management/checks",
        {
          method: "POST",
          body: JSON.stringify(
            fingerprint
              ? { targets }
              : {
                  kind: project,
                  ...(manual ? { manual: true } : {}),
                  targets: targets.map(({ model, ...target }) => ({
                    ...target,
                    models: model ? [model] : [],
                  })),
                },
          ),
        },
      );
      setNotice({
        text: `已提交${detectionProjectLabels[project]} · ${result.queued ?? targets.length} 项`,
      });
      await client.invalidateQueries({
        queryKey: [fingerprint ? "model-fingerprints" : "configured-checks"],
      });
    } catch (error) {
      setNotice({
        text: error instanceof Error ? error.message : "检测提交失败",
        error: true,
      });
    } finally {
      keys.forEach(key => inFlight.current.delete(key));
      setSubmitting(new Set(inFlight.current));
      if (!manual) { batchInFlight.current = false; setBatchActive(false); }
    }
  }

  return {
    project,
    expanded,
    total,
    batchIssue,
    notice,
    active: batchActive,
    select(next: typeof project) {
      setNotice(undefined);
      selectProject(next);
    },
    open: () => setExpanded(true),
    close() {
      if (project === "quality") checks.stop();
      setNotice(undefined);
      setExpanded(false);
    },
    runAll: () =>
      project === "quality"
        ? checks.runAll(qualityRows)
        : !batchIssue && queue([...batchTargets.values()]),
    rowAction(row: Channel) {
      const target = targetFor(row);
      const record = target && capability ? entry(target) : undefined;
      const status = (record?.result as { status?: string } | null)?.status;
      const labels: Record<string, string> =
        project === "compaction" ? compactionLabels : toolUseLabels;
      return {
        applicable: !!target,
        busy: !!target && busy(target),
        status:
          record?.state === "done"
            ? labels[status || ""] || "检测完成"
            : record?.state === "error"
              ? "检测失败"
              : record?.state === "interrupted"
                ? "已中断"
                : record?.state === "queued"
                  ? "已排队"
                  : record?.state === "running"
                    ? "检测中"
                    : "",
        message: record?.message,
        run: () => target && !busy(target) && void queue([target], true),
      };
    },
  };
}

type Detection = ReturnType<typeof useObservedDetection>;

export function ObservedDetectionActions({
  detection,
  checks,
  disabled,
}: {
  detection: Detection;
  checks: Checks;
  disabled: boolean;
}) {
  const quality = detection.project === "quality";
  const runLabel = `一键${quality ? "检测" : detectionProjectLabels[detection.project]} · ${detection.total}`;
  const busy = !!checks.batch || detection.active;
  return (
    <div className="check-toolbar-actions">
      {detection.expanded && checks.batch && (
        <span className="muted" role="status">
          {checks.batch.done} / {checks.batch.total}
        </span>
      )}
      <DetectionAction
        projects={detectionProjects}
        project={detection.project}
        onSelect={detection.select}
        selectDisabled={busy || checks.pending.size > 0}
      >
        {detection.expanded ? (
          <button
            className="button primary small"
            aria-label={runLabel}
            aria-busy={busy}
            title={
              detection.batchIssue || detectionProjectLabels[detection.project]
            }
            disabled={
              disabled ||
              busy ||
              checks.pending.size > 0 ||
              !detection.total ||
              !!detection.batchIssue
            }
            onClick={() => void detection.runAll()}
          >
            {busy ? <Spinner small /> : <ScanLine size={16} />}
            {runLabel}
          </button>
        ) : (
          <button className="button small" onClick={detection.open}>
            <ScanLine size={15} />
            {detectionProjectLabels[detection.project]}
          </button>
        )}
      </DetectionAction>
      {detection.expanded && (
        <button
          className="button small"
          disabled={detection.active}
          onClick={detection.close}
        >
          <X size={14} />
          {quality ? "取消检测" : "收起检测"}
        </button>
      )}
    </div>
  );
}

export function ObservedDetectionRow({
  row,
  detection,
}: {
  row: Channel;
  detection: Detection;
}) {
  const action = detection.rowAction(row);
  return (
    <td>
      <div className="quality-status-stack">
        <button
          className="button small"
          disabled={!action.applicable || action.busy}
          aria-busy={action.busy}
          title={
            detection.project === "fingerprint"
              ? undefined
              : capabilityCheckScope
          }
          aria-label={`重新检测 ${row.source_name || row.source_id || ""} ${channelName(row)} ${row.model}`}
          onClick={action.run}
        >
          {action.busy ? <Spinner small /> : <Play size={13} />}
          {!action.applicable ? "不适用" : action.busy ? "检测中…" : "重新检测"}
        </button>
        {action.status && (
          <small className="muted" title={action.message}>
            {action.status}
          </small>
        )}
      </div>
    </td>
  );
}
