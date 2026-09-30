import type { Result } from "./Sub2apiChecks";

// Quality is an annotation, independent of model availability and selection.
export function ModelQualityWarning({
  model,
  result,
}: {
  model: string;
  result?: Pick<Result, "verdict" | "checked_at"> | null;
}) {
  if (model !== "gpt-6-astra" || result?.verdict !== "fail") return null;
  const checked = result.checked_at
    ? `（${new Date(result.checked_at * 1000).toLocaleString("zh-CN", { hour12: false })}）`
    : "";
  return (
    <small
      className="model-quality-warning"
      title={`此渠道的 gpt-6-astra 降智检测结果为降智${checked}。可用性与降智分别判断。`}
    >
      降智
    </small>
  );
}
