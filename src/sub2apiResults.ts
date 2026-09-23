import { SUB_MODELS } from "./sub2apiModels";
import type { SubTarget } from "./Sub2apiChecks";

export type SubModelCheck = NonNullable<SubTarget["models"]>[number];

// Each result belongs to one model. Only Astra had a legacy group-level result;
// an absent sibling entry must stay untested instead of inheriting that result.
export function modelChecks(target: SubTarget): SubModelCheck[] {
  return [...new Set([...SUB_MODELS, ...(target.models || []).map(c => c.model)])].map((model) => {
    const saved = target.models?.find((item) => item.model === model);
    if (saved) return saved;
    const result = model === "gpt-6-astra" ? target.result : null;
    return {
      model,
      state: result ? "done" : "idle",
      message: "",
      result,
    };
  });
}

export function availableModelChecks(target: SubTarget): SubModelCheck[] {
  return modelChecks(target).filter(
    (check) =>
      check.state === "done" && check.result?.availability.status === "success",
  );
}

export function availabilityCounts(checks: SubModelCheck[]) {
  return {
    success: checks.filter((c) => c.result?.availability.status === "success")
      .length,
    failed: checks.filter((c) => c.result?.availability.status === "error")
      .length,
    untested: checks.filter((c) => !c.result).length,
    pending: checks.filter((c) => ["queued", "running"].includes(c.state))
      .length,
    interrupted: checks.filter((c) => c.state === "interrupted").length,
  };
}

export function importModelLabel(check: SubModelCheck): string {
  if (check.state === "queued") return "排队中";
  if (check.state === "running") return "检测中";
  if (check.state === "interrupted") return "已中断";
  if (check.state === "error" || check.result?.availability.status === "error")
    return "检测失败";
  if (check.state === "done" && check.result?.availability.status === "success")
    return "可用";
  return "未检测";
}

export const modelMatchLabels = {
  match: "匹配",
  mismatch: "不匹配",
  missing: "未返回",
  invalid: "格式无效",
  unavailable: "无法判定",
  legacy: "待补测",
  untested: "未检测",
} as const;

export function modelMatchStatus(
  check: SubModelCheck,
): keyof typeof modelMatchLabels {
  if (!check.result) return "untested";
  const status = check.result.availability.model_match;
  if (!status) return "legacy";
  if (check.result.availability.status !== "success") return "unavailable";
  return status;
}
