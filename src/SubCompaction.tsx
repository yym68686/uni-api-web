import { isCapabilityCheckModel, capabilityCheckScope } from "./sub2apiModels";
import { Check, CircleHelp, X } from "lucide-react";
import { Tip, Spinner } from "./ui";
import { time } from "./format";
import type { Probe, SubTarget } from "./Sub2apiChecks";

export interface CompactionResult {
  status: "supported" | "unsupported" | "error" | "not_applicable";
  model?: string;
  message?: string;
  checked_at: number;
  attempts: Probe[];
}
export function compactionStatus(target: SubTarget, model?: string) {
  if (model && !isCapabilityCheckModel(model)) return "not_applicable";
  if (target.compaction?.model && !isCapabilityCheckModel(target.compaction.model)) return "untested";
  return target.compaction?.status || "untested";
}
export const compactionLabels = {
  supported: "支持压缩", unsupported: "不支持压缩", error: "检测失败", untested: "未检测", not_applicable: "不适用",
};
export function CompactionStatus({ target, model }: { target: SubTarget; model?: string }) {
  if (model && !isCapabilityCheckModel(model)) return <Tip text={capabilityCheckScope}><span className="check-status inconclusive">不适用</span></Tip>;
  const status = compactionStatus(target, model);
  const result = target.compaction;
  const running = ["queued", "running"].includes(target.compaction_state || "");
  return <Tip text={[
    result?.model && `已验证模型：${result.model}`,
    result?.message,
    result?.checked_at && `检测于 ${time(result.checked_at)}`,
    running && "正在检测，显示上次结果",
    target.compaction_state === "interrupted" && "上次检测已中断，可重新检测",
  ].filter(Boolean).join("；") || "自动选择渠道已验证可用的 gpt 或 codex 模型，任一模型返回有效压缩输出即为支持"}>
    <span className={`check-status ${status === "supported" ? "pass" : status === "unsupported" ? "fail" : "inconclusive"}`}>
      {running ? <Spinner small /> : status === "supported" ? <Check size={15} /> : status === "unsupported" ? <X size={15} /> : <CircleHelp size={15} />}
      {running && !result ? "检测中" : compactionLabels[status]}
    </span>
  </Tip>;
}
