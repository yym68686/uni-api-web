export const observationColumns = [
  { id: "status", label: "路由状态", className: "" },
  { id: "inflight", label: "当前并发", className: "column-run observation-inflight" },
  { id: "success", label: "尝试成功率", className: "column-run column-success" },
  { id: "attempts", label: "尝试数", className: "column-quality" },
  { id: "wait", label: "请求前等待", className: "column-quality" },
  { id: "latency", label: "首字延迟", className: "column-run" },
  { id: "duration", label: "总时间", className: "column-quality" },
  { id: "usage", label: "Token / 缓存率", className: "column-finance" },
  { id: "estimated", label: "估算消费", className: "column-finance" },
  { id: "actual", label: "实际消费", className: "column-finance" },
  { id: "profit", label: "利润参考", className: "column-finance" },
  { id: "balance", label: "站点余额", className: "column-balance" },
  { id: "quality", label: "是否降智", className: "column-quality" },
  { id: "fingerprint", label: "指纹", className: "column-quality" },
] as const;

export type ObservationColumn = typeof observationColumns[number]["id"];
export type DetectionColumn = "quality" | "fingerprint";
export const observationColumnClass = Object.fromEntries(observationColumns.map(({ id, className }) => [id, className])) as Record<ObservationColumn, string>;
export type ChannelMetricLayout = "run" | "quality" | "finance" | "all";

export function observationPreset(layout: ChannelMetricLayout, compact = false, account = false): ObservationColumn[] {
  if (compact && layout === "run") return ["status", "success", "balance"];
  const detection: ObservationColumn[] = account && (layout === "quality" || layout === "all") ? ["quality", "fingerprint"] : [];
  return [...detection, ...observationColumns.filter(({ id, className }) =>
    id !== "quality" && id !== "fingerprint" && (layout === "all" || id === "status" || id === "balance" || className.startsWith(`column-${layout}`)),
  ).map(({ id }) => id)];
}
