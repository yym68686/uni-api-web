import { LoadingIndicator } from "./LoadingIndicator";
import type { View } from "./preferences";

const tableLabels = {
  channels: "渠道",
  balances: "余额",
  requests: "请求日志",
  accounts: "站点账号",
  checks: "渠道检测",
  prices: "模型价格",
  history: "历史记录",
};

export function TableLoading({ kind }: { kind: keyof typeof tableLabels }) {
  return <LoadingIndicator size="panel" label={`加载${tableLabels[kind]}`} />;
}

export function DetailLoading({ kind = "form", label = "详情" }: {
  kind?: "form" | "timeline" | "chart" | "models";
  label?: string;
}) {
  return <LoadingIndicator size={kind === "models" ? "section" : "panel"} label={`加载${label}`} />;
}

export function SourceLoading() {
  return <LoadingIndicator size="panel" label="加载来源设置" />;
}

export function TaskLoading() {
  return <LoadingIndicator size="panel" label="加载自动化任务" />;
}

const pageLabels: Record<View, string> = {
  overview: "统计指标",
  routing: "路由顺序",
  channels: "渠道",
  balances: "余额",
  requests: "请求日志",
  sub2api: "渠道检测",
  prices: "模型价格",
  sources: "设置",
  automations: "自动化",
};

export function PageLoading({ view, label }: { view: View; label?: string }) {
  return <LoadingIndicator className="page-loading" label={label ?? `加载${pageLabels[view]}`} />;
}
