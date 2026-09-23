import { Check, CircleHelp, X } from "lucide-react";
import { Tip, Spinner } from "./ui";
import { time } from "./format";
import type { SubTarget } from "./Sub2apiChecks";
import {
  modelToolUse,
  toolUseCandidates,
  toolUseModels,
  toolUseStatus,
  toolUseDescription,
  toolUseLabels,
} from "./toolUse";
export { toolUseStatus, toolUseDescription, toolUseLabels } from "./toolUse";
export type { ToolUseResult } from "./toolUse";

export function ToolUseStatus({
  target,
  model,
}: {
  target: SubTarget;
  model?: string;
}) {
  const result = model ? modelToolUse(target, model) : target.tool_use,
    status = toolUseStatus(target, model);
  const state = toolUseModels(target.tool_use).find(
    (m) => m.model === model,
  )?.state;
  const running =
    ["queued", "running"].includes(target.tool_use_state || "") &&
    (!model || !state || ["queued", "running"].includes(state));
  const candidates = toolUseCandidates(target);
  const passed = candidates.filter(
    (m) => modelToolUse(target, m)?.status === "supported",
  ).length;
  return (
    <Tip
      text={[
        result?.model && `检测模型：${result.model}`,
        result?.message,
        result?.checked_at && `检测于 ${time(result.checked_at)}`,
        running && "正在检测，显示上次结果",
        target.tool_use_state === "interrupted" && "上次检测已中断，可重新检测",
        toolUseDescription,
      ]
        .filter(Boolean)
        .join("；")}
    >
      <span
        className={`check-status ${status === "supported" ? "pass" : status === "unsupported" ? "fail" : "inconclusive"}`}
      >
        {running ? (
          <Spinner small />
        ) : status === "supported" ? (
          <Check size={15} />
        ) : status === "unsupported" ? (
          <X size={15} />
        ) : (
          <CircleHelp size={15} />
        )}
        {running && !result
          ? "检测中"
          : !model && candidates.length > 1
            ? `${passed}/${candidates.length} 模型支持`
            : toolUseLabels[status]}
      </span>
    </Tip>
  );
}
