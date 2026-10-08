import type { CSSProperties, ReactNode } from "react";
import {
  Activity,
  Bot,
  Globe2,
  List,
  Server,
  SlidersHorizontal,
  Wallet,
} from "lucide-react";
import type { View } from "./preferences";
import "./pageLoading.css";

type TableKind =
  | "channels"
  | "balances"
  | "requests"
  | "accounts"
  | "checks"
  | "prices"
  | "history"
  | "modelSummary"
  | "channelSummary";
const columns: Record<TableKind, string[]> = {
  channels: [
    "渠道 / 模型",
    "是否降智",
    "状态",
    "成功率",
    "首字延迟",
    "Token / 缓存率",
    "估算消费",
  ],
  balances: [
    "渠道",
    "模型 / 优先级",
    "成功率",
    "首字",
    "总耗时",
    "估算消费",
    "渠道实际消费",
    "利润",
    "余额",
  ],
  requests: [
    "时间 / 请求 ID",
    "来源 / API key",
    "模型 / 端点",
    "最终结果",
    "总耗时",
    "首字延迟",
    "请求前等待",
    "输入 / 输出 token",
  ],
  accounts: ["站点 / 网址", "账号", "余额", "同步状态", "操作"],
  checks: [
    "渠道 / 站点",
    "模型",
    "倍率",
    "可用性",
    "首字延迟",
    "模型匹配",
    "Tool use",
    "最近检测",
  ],
  prices: [
    "模型",
    "输入",
    "输出",
    "缓存读取",
    "缓存写入",
    "售卖比例",
    "长上下文",
    "操作",
  ],
  history: ["时间", "任务 / 事件", "结果", "详情"],
  modelSummary: ["模型", "Token", "估算消费"],
  channelSummary: ["渠道", "模型", "并发"],
};
const names: Record<TableKind, string> = {
  channels: "渠道",
  balances: "余额",
  requests: "请求日志",
  accounts: "站点账号",
  checks: "渠道检测",
  prices: "模型价格",
  history: "历史记录",
  modelSummary: "模型消费",
  channelSummary: "当前渠道",
};
function Bone({
  width = "70%",
  className = "",
}: {
  width?: string;
  className?: string;
}) {
  return <span className={`loading-bone ${className}`} style={{ width }} />;
}
function LoadingFrame({
  label,
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`content-loading ${className}`}
      role="status"
      aria-label={`加载${label}`}
      aria-busy="true"
    >
      <span className="sr-only">{label}加载中</span>
      <div aria-hidden="true">{children}</div>
    </div>
  );
}
export function TableLoading({
  kind,
  rows = 6,
}: {
  kind: TableKind;
  rows?: number;
}) {
  const fields = columns[kind];
  return (
    <LoadingFrame
      label={names[kind]}
      className={`loading-table loading-table-${kind}`}
    >
      <div
        className="loading-table-inner"
        style={{ "--loading-columns": fields.length } as CSSProperties}
      >
        <div className="loading-table-heading">
          {fields.map((field) => (
            <span key={field}>{field}</span>
          ))}
        </div>
        {Array.from({ length: rows }, (_, index) => (
          <div className="loading-table-row" key={index}>
            {fields.map((field, column) => (
              <div
                className={`loading-cell ${column === 0 ? "loading-identity" : ""}`}
                key={field}
              >
                {column === 0 &&
                  ["channels", "balances", "accounts", "checks"].includes(
                    kind,
                  ) && <Bone className="loading-avatar" width="32px" />}
                <div>
                  <Bone
                    width={`${[72, 54, 83, 62][(index + column) % 4]}%`}
                    className={
                      column > 1 && (column + index) % 4 === 0
                        ? "loading-pill"
                        : ""
                    }
                  />
                  {(column === 0 || (kind === "requests" && column < 3)) && (
                    <Bone width="45%" className="loading-secondary" />
                  )}
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </LoadingFrame>
  );
}
export function MetricLoading({ overview = false }: { overview?: boolean }) {
  const labels = overview
    ? ["请求数量", "渠道尝试", "Token 数量", "估算消费", "渠道总并发", "缓存率"]
    : ["观测渠道", "可用渠道", "渠道成功率", "余额不足"];
  return (
    <LoadingFrame
      label="统计指标"
      className={`loading-metrics ${overview ? "loading-overview-metrics" : ""}`}
    >
      <div className={overview ? "overview-grid" : "metric-grid"}>
        {labels.map((label) => (
          <div className="metric-card" key={label}>
            <div className="metric-card-label">{label}</div>
            <Bone className="loading-metric-value" width="46%" />
            <Bone width="66%" />
          </div>
        ))}
      </div>
    </LoadingFrame>
  );
}
export function DetailLoading({
  kind = "form",
  label = "详情",
}: {
  kind?: "form" | "timeline" | "chart" | "models";
  label?: string;
}) {
  return (
    <LoadingFrame
      label={label}
      className={`loading-detail loading-detail-${kind}`}
    >
      {kind === "chart" ? (
        <div className="loading-chart">
          <Bone width="26%" />
          <div className="loading-chart-grid" />
          <div className="loading-chart-axis">
            <Bone width="16%" />
            <Bone width="16%" />
            <Bone width="16%" />
          </div>
        </div>
      ) : kind === "timeline" ? (
        <>
          <div className="loading-trace-summary">
            {Array.from({ length: 4 }, (_, i) => (
              <div key={i}>
                <Bone width="45%" />
                <Bone width="70%" className="loading-metric-value" />
              </div>
            ))}
          </div>
          <div className="loading-timeline">
            {Array.from({ length: 3 }, (_, i) => (
              <div key={i}>
                <span className="loading-timeline-dot" />
                <Bone width="32%" />
                <Bone
                  width={`${85 - i * 12}%`}
                  className="loading-timeline-bar"
                />
              </div>
            ))}
          </div>
        </>
      ) : kind === "models" ? (
        <div className="loading-models">
          {Array.from({ length: 6 }, (_, i) => (
            <div key={i}>
              <Bone width="13px" className="loading-checkbox" />
              <Bone width="65%" />
            </div>
          ))}
        </div>
      ) : (
        <div className="loading-form">
          {Array.from({ length: 4 }, (_, i) => (
            <div key={i}>
              <Bone width="30%" />
              <Bone width="100%" className="loading-input" />
            </div>
          ))}
        </div>
      )}
    </LoadingFrame>
  );
}
export function SourceLoading() {
  return (
    <LoadingFrame label="来源设置" className="loading-sources">
      {Array.from({ length: 3 }, (_, i) => (
        <div className="loading-source-row" key={i}>
          <Bone className="loading-avatar" width="38px" />
          <div>
            <Bone width="32%" />
            <Bone width="64%" className="loading-secondary" />
          </div>
          <Bone width="78px" className="loading-input" />
        </div>
      ))}
    </LoadingFrame>
  );
}
export function TaskLoading() {
  return (
    <LoadingFrame label="自动化任务" className="loading-tasks">
      {Array.from({ length: 3 }, (_, i) => (
        <div className="loading-task-row" key={i}>
          <Bone width="30%" />
          <Bone width="56%" className="loading-secondary" />
          <Bone width="38%" className="loading-secondary" />
          <span className="loading-task-state">
            <Bone width="58px" className="loading-pill" />
          </span>
        </div>
      ))}
    </LoadingFrame>
  );
}
function Panel({
  title,
  icon,
  filters = 0,
  children,
}: {
  title: string;
  icon: ReactNode;
  filters?: number;
  children: ReactNode;
}) {
  return (
    <section className="data-panel loading-panel">
      <div className="data-heading">
        <div className="data-title">
          {icon}
          <h2>{title}</h2>
        </div>
        <div className="loading-toolbar" aria-hidden="true">
          <Bone width="180px" className="loading-input" />
          <Bone width="82px" className="loading-input" />
        </div>
      </div>
      {filters > 0 && (
        <div className="loading-filters" aria-hidden="true">
          {Array.from({ length: filters }, (_, i) => (
            <Bone
              width={`${i === 2 ? 200 : 100 + (i % 2) * 28}px`}
              className="loading-input"
              key={i}
            />
          ))}
        </div>
      )}
      {children}
    </section>
  );
}
export function PageLoading({ view }: { view: View }) {
  let content: ReactNode;
  switch (view) {
    case "overview":
      content = (
        <>
          <MetricLoading overview />
          <div className="loading-overview-lists">
            <Panel title="模型消费" icon={<Wallet size={18} />}>
              <TableLoading kind="modelSummary" rows={4} />
            </Panel>
            <Panel title="当前渠道" icon={<Activity size={18} />}>
              <TableLoading kind="channelSummary" rows={4} />
            </Panel>
          </div>
        </>
      );
      break;
    case "channels":
    case "balances":
      content = (
        <>
          <MetricLoading />
          <Panel
            title={view === "channels" ? "渠道表现" : "渠道余额"}
            icon={
              view === "channels" ? (
                <Activity size={19} />
              ) : (
                <Wallet size={19} />
              )
            }
            filters={6}
          >
            <TableLoading kind={view} />
          </Panel>
        </>
      );
      break;
    case "requests":
      content = (
        <Panel title="请求日志" icon={<List size={19} />} filters={7}>
          <TableLoading kind="requests" rows={8} />
        </Panel>
      );
      break;
    case "sub2api":
      content = (
        <>
          <Panel title="站点账号" icon={<Globe2 size={19} />}>
            <TableLoading kind="accounts" rows={3} />
          </Panel>
          <Panel title="渠道管理" icon={<Activity size={19} />} filters={6}>
            <TableLoading kind="checks" rows={4} />
          </Panel>
        </>
      );
      break;
    case "prices":
      content = (
        <Panel title="模型价格" icon={<SlidersHorizontal size={19} />}>
          <TableLoading kind="prices" rows={8} />
        </Panel>
      );
      break;
    case "sources":
      content = (
        <Panel title="uni-api 来源" icon={<Server size={19} />}>
          <SourceLoading />
          <div className="loading-settings-form">
            <Bone width="100px" />
            <DetailLoading kind="form" label="账户设置" />
          </div>
        </Panel>
      );
      break;
    case "automations":
      content = (
        <>
          <Panel title="自动化" icon={<Bot size={19} />}>
            <TaskLoading />
          </Panel>
          <Panel title="历史与变动审计" icon={<Activity size={19} />}>
            <TableLoading kind="history" rows={4} />
          </Panel>
        </>
      );
      break;
  }
  return (
    <div className="page-loading" data-loading-view={view} aria-busy="true">
      {content}
    </div>
  );
}
