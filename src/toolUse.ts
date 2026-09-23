import type { SubTarget } from "./Sub2apiChecks";
import type { CompactionResult } from "./SubCompaction";

export interface ToolUseModel {
  model: string;
  state: string;
  result?: CompactionResult | null;
}
export interface ToolUseResult extends CompactionResult {
  models?: ToolUseModel[];
}
export const toolUseLabels = {
  supported: "支持工具调用",
  unsupported: "不支持工具调用",
  error: "检测失败",
  untested: "未检测",
};
export const toolUseDescription =
  "逐个检测渠道全部已验证可用的模型。使用 Codex additional_tools 中的 custom exec，要求返回指定工具调用与输入；结果仅代表对应模型的本次 exec 检测。";
export function toolUseModels(result?: ToolUseResult): ToolUseModel[] {
  if (result?.models) return result.models;
  // Older channel-wide records are evidence for exactly one named model.
  return result?.model ? [{ model: result.model, state: "done", result }] : [];
}
export function modelToolUse(
  target: Pick<SubTarget, "tool_use">,
  model: string,
) {
  return (
    toolUseModels(target.tool_use).find((m) => m.model === model)?.result ||
    undefined
  );
}
export function toolUseFailed(
  target: Pick<SubTarget, "tool_use">,
  model: string,
) {
  const status = modelToolUse(target, model)?.status;
  return status === "unsupported" || status === "error";
}
export function toolUseCandidates(target: SubTarget) {
  return [
    ...new Set([
      ...(target.models || [])
        .filter((m) => m.result?.availability.status === "success")
        .map((m) => m.model),
      ...(target.result?.availability.status === "success"
        ? [target.result.model]
        : []),
      ...toolUseModels(target.tool_use).map((m) => m.model),
    ]),
  ];
}
export function toolUseStatus(target: SubTarget, model?: string) {
  if (model) return modelToolUse(target, model)?.status || "untested";
  const statuses = toolUseCandidates(target).map(
    (m) => modelToolUse(target, m)?.status || "untested",
  );
  if (statuses.includes("unsupported")) return "unsupported";
  if (statuses.includes("error")) return "error";
  if (!statuses.length || statuses.includes("untested")) return "untested";
  return "supported";
}
export function toolUseMatches(
  target: SubTarget,
  filter: string,
  model?: string,
) {
  if (!filter) return true;
  if (model) return toolUseStatus(target, model) === filter;
  // A failed/unknown model must remain discoverable in a mixed channel.
  const candidates = toolUseCandidates(target);
  return filter === "supported"
    ? toolUseStatus(target) === filter
    : candidates.length
      ? candidates.some((m) => toolUseStatus(target, m) === filter)
      : toolUseStatus(target) === filter;
}
