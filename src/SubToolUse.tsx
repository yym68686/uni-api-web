import { Check, CircleHelp, X } from "lucide-react";
import { Tip, Spinner } from "./ui";
import { time } from "./format";
import type { SubTarget } from "./Sub2apiChecks";
import type { CompactionResult } from "./SubCompaction";

export type ToolUseResult = CompactionResult;
export const toolUseLabels = {
  supported: "支持工具调用", unsupported: "不支持工具调用", error: "检测失败", untested: "未检测",
};
export const toolUseDescription = "检测 Codex additional_tools 中的 custom exec：必须返回指定工具调用和输入；NO_EXEC、普通文本或错误工具调用均未通过。结果仅代表本次所选模型，不证明上游删除了工具。";
export const toolUseStatus = (target: SubTarget) => target.tool_use?.status || "untested";

export function ToolUseStatus({ target }: { target: SubTarget }) {
  const result = target.tool_use, status = toolUseStatus(target);
  const running = ["queued", "running"].includes(target.tool_use_state || "");
  return <Tip text={[
    result?.model && `检测模型：${result.model}`,
    result?.message,
    result?.checked_at && `检测于 ${time(result.checked_at)}`,
    running && "正在检测，显示上次结果",
    target.tool_use_state === "interrupted" && "上次检测已中断，可重新检测",
    toolUseDescription,
  ].filter(Boolean).join("；")}>
    <span className={`check-status ${status === "supported" ? "pass" : status === "unsupported" ? "fail" : "inconclusive"}`}>
      {running ? <Spinner small /> : status === "supported" ? <Check size={15} /> : status === "unsupported" ? <X size={15} /> : <CircleHelp size={15} />}
      {running && !result ? "检测中" : toolUseLabels[status]}
    </span>
  </Tip>;
}
