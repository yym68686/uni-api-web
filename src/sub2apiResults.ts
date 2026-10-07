import { SUB_MODELS } from "./sub2apiModels";
import type { SubTarget, Result, Probe } from "./Sub2apiChecks";

export type SubModelCheck = NonNullable<SubTarget["models"]>[number];

export function probeHasWarning(probe?: Probe): boolean {
  return probe?.status === "success" && !!probe.terminal_status && probe.terminal_status !== "complete";
}

// Selectable warnings require an explicit user choice when creating routes.
export function modelIsDefaultSelected(check?: SubModelCheck): boolean {
  return check?.state === "done" && check.result?.availability.status === "success" && !probeHasWarning(check.result.availability);
}

// Each result belongs to one model. Only Astra had a legacy group-level result;
// an absent sibling entry must stay untested instead of inheriting that result.
export function modelChecks(target: SubTarget): SubModelCheck[] {
  const byModel = new Map((target.models || []).map(check => [check.model, check]));
  return [...new Set([...SUB_MODELS, ...(target.models || []).map(c => c.model)])].map((model) => {
    const saved = byModel.get(model);
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
    warning: checks.filter(c => probeHasWarning(c.result?.availability)).length,
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
    return check.result.availability.terminal_status === "missing"
      ? "成功，但缺少结束事件"
      : check.result.availability.terminal_status === "missing_output"
        ? "成功，但结束事件缺少最终文本" : "可用";
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

// Shared quality is separate from model availability and its billing details.
export function groupQualityResult(target: SubTarget): Result | null {
  const saved = modelChecks(target).find(c => c.model === "gpt-6-astra")?.result || null;
  const check = target.quality_check;
  if (!check) return saved;
  // Receipt lookups enrich the saved native probe after history was recorded.
  // Keep that newer billing detail when both refer to the same native check.
  if (check.quality_probe && saved?.checked_at === check.checked_at &&
      saved.quality.id === check.quality_probe.id) return saved;
  const unavailable: Probe = { status: "skipped", text: "", ttft_ms: null, duration_ms: 0 };
  return { model: "gpt-6-astra", checked_at: check.checked_at, verdict: check.verdict,
    availability: saved?.availability || unavailable,
    quality: check.quality_probe || { curl_token: check.curl_token, status: check.verdict === "error" ? "error" : "success", text: check.text, message: check.message, ttft_ms: null, duration_ms: check.duration_ms },
  };
}
