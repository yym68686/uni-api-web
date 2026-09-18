import { BalanceAmount } from "./BalanceAmount";
import { LatencyBadge } from "./LatencyBadge";
import { CircleHelp } from "lucide-react";
import type { Balance, Channel, Distribution } from "./types";
import { Tip, Spinner } from "./ui";
import {
  balanceKind,
  balanceLabel,
  balanceStatus,
  time,
  ms,
  count,
  rate,
  reasonLabel,
} from "./format";
import { usd } from "./analytics";
import type { ActualCostRange } from "./actualCost";
export interface BalanceResult {
  data?: Balance;
  isPending?: boolean;
  isError?: boolean;
}
export function BalanceValue({
  balance,
  loading,
  failed,
  detail = false,
}: {
  balance?: Balance;
  loading?: boolean;
  failed?: boolean;
  detail?: boolean;
}) {
  if (loading && !balance)
    return (
      <span className="loading-text">
        <Spinner small />
        查询中
      </span>
    );
  if (!balance || failed) return <span className="muted">查询失败</span>;
  if (!balance.keys?.length)
    return (
      <span className="muted">
        {balanceStatus[balance.status] || "暂无数据"}
      </span>
    );
  return (
    <div className={`balance-values ${detail ? "expanded" : ""}`}>
      {balance.keys.map((item) => (
        <Tip
          key={item.position}
          text={
            <>
              <strong>{balanceKind[item.kind || ""] || "余额查询"}</strong>
              <br />
              {item.checked_at
                ? `查询时间 ${time(item.checked_at)} · 最多缓存 5 分钟`
                : "上游尚未提供金额"}
              <br />
              同一账户的多个渠道余额可能共享，不可相加。
            </>
          }
        >
          <span className="amount">
            {balance.keys!.length > 1 && <small>Key {item.position} </small>}
            {item.status !== "ok" || item.unlimited ? (
              balanceLabel(item)
            ) : typeof item.amount === "number" &&
              Number.isFinite(item.amount) ? (
              <BalanceAmount
                value={item.amount}
                currency={item.currency || "USD"}
              />
            ) : item.windows?.length ? (
              item.windows.map((window, index) => (
                <span key={window.window}>
                  {index > 0 && " / "}
                  {window.window} ·{" "}
                  <BalanceAmount
                    value={window.remaining}
                    currency={item.currency || "USD"}
                  />
                </span>
              ))
            ) : (
              <span className="muted">{balanceLabel(item)}</span>
            )}
            {detail && (
              <small className="balance-kind">
                {balanceKind[item.kind || ""]}
              </small>
            )}
          </span>
        </Tip>
      ))}
      {!!balance.omitted_keys && (
        <small>另 {balance.omitted_keys} 个密钥未查询</small>
      )}
    </div>
  );
}
export function Timing({
  value,
  text,
  wait = false,
}: {
  value?: Distribution;
  text?: Distribution;
  wait?: boolean;
}) {
  return (
    <Tip
      text={
        <>
          <strong>
            {wait
              ? "请求进入 uni-api → 渠道 HTTP 发起前"
              : "渠道请求发起 → 首个 response.created"}
          </strong>
          <br />
          p95 {ms(value?.p95_ms)} · 最近 {ms(value?.last_ms)}
          <br />
          {count(value?.sample_count || 0)} 次样本 · 分位值为直方图上界估计
          {!wait && (
            <>
              <br />
              首个 response.output_text.delta：p50 {ms(text?.p50_ms)} · p95{" "}
              {ms(text?.p95_ms)} · 最近 {ms(text?.last_ms)}
              <br />
              {count(text?.sample_count || 0)} 次正文首字样本
            </>
          )}
          {wait && (
            <>
              <br />
              包含读包、排队与前序重试；不包含入口前耗时。
            </>
          )}
        </>
      }
    >
      {wait ? (
        <span
          className={`metric-value ${value?.p50_ms == null ? "muted" : ""}`}
        >
          {ms(value?.p50_ms)}
        </span>
      ) : (
        <LatencyBadge value={value?.p50_ms} />
      )}
    </Tip>
  );
}
export function Status({ row }: { row: Channel }) {
  return (
    <span className={`status-pill ${row.eligible ? "healthy" : "cooling"}`}>
      <span className="tiny-dot" />
      {reasonLabel[row.reason] || (row.eligible ? "可用" : "不可用")}
    </span>
  );
}
export function ChannelMetricHeaders({
  keySelected = false,
}: {
  keySelected?: boolean;
}) {
  return (
    <>
      <th>状态</th>
      <th>
        <Tip text="当前正在请求此渠道的全部 API key 的尝试数，不受历史时间窗口或 API key 用量筛选影响。">
          {keySelected ? "渠道总并发" : "当前并发"} <CircleHelp size={12} />
        </Tip>
      </th>
      <th>
        <Tip text="成功与失败的渠道尝试分别计数，重试不是新的用户请求。">
          成功率 <CircleHelp size={12} />
        </Tip>
      </th>
      <th>尝试数</th>
      <th>
        <Tip text="请求进入 uni-api → 渠道 HTTP 发起前。包含前序重试耗时，悬停或展开查看详情。">
          请求前等待 <small>p50</small>
        </Tip>
      </th>
      <th>
        首字延迟 <small>p50 / p95</small>
      </th>
      <th>Token / 缓存率</th>
      <th>估算消费</th>
      <th>
        <Tip text="来自上游 sub2api 的 actual_cost，按日历日统计；充值增加不会计入消费。同一上游账号可能被多个 uni-api 共用，这一金额不按 uni-api 的调用 API key、来源或模型拆分，不可跨来源相加。5 分钟、15 分钟和 1 小时窗口没有可验证的上游小时账单。">
          {keySelected ? "渠道实际消费" : "实际消费"} <CircleHelp size={12} />
        </Tip>
      </th>
      <th>余额 / 额度</th>
    </>
  );
}
export function ChannelMetricCells({
  row,
  inflight,
  balance,
  actualRange,
  stale = false,
  metricsUnavailable = false,
}: {
  row: Channel;
  inflight?: number | null;
  balance?: BalanceResult;
  actualRange: ActualCostRange;
  stale?: boolean;
  metricsUnavailable?: boolean;
}) {
  const success = row.stats?.success_rate;
  return (
    <>
      <td>
        <Status row={row} />
      </td>
      <td className="mono">{inflight == null ? "—" : inflight}</td>
      <td>
        <div className="success-cell">
          <span
            className={`mono ${success == null ? "muted" : success < 0.5 ? "negative" : ""}`}
          >
            {rate(success)}
          </span>
          <span className="rate-track">
            <i
              className={success != null && success < 0.5 ? "low" : ""}
              style={{
                width: `${(success || 0) * 100}%`,
              }}
            />
          </span>
        </div>
      </td>
      <td className="mono">
        {metricsUnavailable
          ? "—"
          : row.history_configured === false
            ? "未接入"
            : stale && !row.stats?.success_rate_denominator
              ? "未同步"
              : count(row.stats?.success_rate_denominator || 0)}
      </td>
      <td>
        <Timing value={row.stats?.request_to_dispatch} wait />
      </td>
      <td>
        <div className="dual-metric">
          <Timing
            value={row.stats?.response_created}
            text={row.stats?.first_text}
          />
          <span className="muted mono">
            {ms(row.stats?.response_created?.p95_ms)}
          </span>
        </div>
      </td>
      <td className="mono">
        {row.stats?.usage_samples
          ? count(
              (row.stats.input_tokens || 0) + (row.stats.output_tokens || 0),
            )
          : "—"}
        <small className="usage-cache">{rate(row.stats?.cache_rate)}</small>
      </td>
      <td className="mono">{usd(row.stats?.estimated_cost_usd)}</td>
      <td className="mono">
        {!actualRange.supported ? (
          <Tip text="sub2api 只提供按日聚合的 actual_cost，当前滚动窗口不显示整日金额。">
            <span className="muted">按日</span>
          </Tip>
        ) : balance?.isPending && !balance.data ? (
          <span className="muted">查询中</span>
        ) : balance?.data?.actual_cost_usd == null ? (
          <span className="muted">—</span>
        ) : (
          usd(balance.data.actual_cost_usd)
        )}
      </td>
      <td>
        <BalanceValue
          balance={balance?.data}
          loading={balance?.isPending}
          failed={balance?.isError}
        />
      </td>
    </>
  );
}
