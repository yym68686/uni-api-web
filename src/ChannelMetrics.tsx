import { Fragment, type ReactNode } from "react";
import { channelLatency, latencyLabel } from "./channelLatency";
import { FailureCounts } from "./FailureCounts";
import { BalanceAmount } from "./BalanceAmount";
import { LatencyBadge } from "./LatencyBadge";
import { CircleHelp } from "lucide-react";
import type { Balance, Channel, Distribution } from "./types";
import { Tip, Spinner } from "./ui";
import {
  balanceKind,
  balanceLabel,
  balanceStatus,
  dateTime,
  ms,
  count,
  rate,
  reasonLabel,
} from "./format";
import { usd } from "./analytics";
import type { ActualCostRange } from "./actualCost";
import { SubChannelSpendValue } from "./SubChannelSpend";
import type { SubChannelSpendResult } from "./SubChannelSpend";
import { salePercent } from "./modelPrices";
import { ChannelProfit, channelProfitCost } from "./ChannelProfit";
import { observationPreset, type ChannelMetricLayout, type ObservationColumn, type DetectionColumn } from "./observationColumns";
export interface BalanceResult {
  data?: Balance;
  isPending?: boolean;
  isError?: boolean;
}
type MetricColumnsProps = {
  layout?: ChannelMetricLayout;
  compact?: boolean;
  columns?: readonly ObservationColumn[];
  renderDetection?: (id: DetectionColumn) => ReactNode;
};
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
      {balance.keys.map((item, index) => (
        <Tip
          key={`${item.position ?? ""}:${index}`}
          keyboard={detail}
          text={
            <>
              <strong>{balanceKind[item.kind || ""] || "余额查询"}</strong>
              <br />
              {item.http_status && item.status !== "ok" && (
                <>上游余额接口返回 HTTP {item.http_status}<br /></>
              )}
              {item.checked_at
                ? `查询时间 ${dateTime(item.checked_at)} · 最多缓存 5 分钟`
                : "上游尚未提供金额"}
              <br />
              同一账户的多个渠道余额可能共享，不可相加。
            </>
          }
        >
          <span className="amount">
            {item.label ? (
              <small>{item.label} </small>
            ) : (
              balance.keys!.length > 1 && <small>Key {item.position} </small>
            )}
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
  kind = "created",
}: {
  value?: Distribution;
  text?: Distribution;
  wait?: boolean;
  kind?: "created" | "output";
}) {
  return (
    <Tip
      keyboard={false}
      text={
        <>
          <strong>
            {wait
              ? "请求进入 uni-api → 渠道 HTTP 发起前"
              : `渠道请求发起 → ${latencyLabel(kind)}`}
          </strong>
          <br />
          p95 {ms(value?.p95_ms)} · 最近 {ms(value?.last_ms)}
          <br />
          {count(value?.sample_count || 0)} 次样本 · 分位值为直方图上界估计
          {!wait && !value?.sample_count && (
            <>
              <br />
              当前范围未采集此延迟时间；成功次数不代表有延迟样本。
              旧版协议转换遗漏的时间无法从成功率或总耗时补算。
            </>
          )}
          {!wait && (
            <>
              <br />
              首个上游正文片段：p50 {ms(text?.p50_ms)} · p95{" "}
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
      ) : !value?.sample_count && value?.p50_ms == null ? (
        <span className="muted">未记录</span>
      ) : (
        <span><LatencyBadge value={value?.p50_ms} />{kind === "output" && <small className="muted"> 首个输出</small>}</span>
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
export function TotalTiming({ value, attempts = 0 }: { value?: Distribution; attempts?: number }) {
  const samples = value?.sample_count || 0;
  const recorded = samples > 0;
  return (
    <Tip keyboard={false} text={<>
      <strong>当前渠道尝试开始 → 离开当前渠道</strong><br />
      包含成功、失败和取消的已结束尝试；流式请求计到流结束。
      不包含尝试此渠道之前的排队、前序重试，也不包含后续渠道耗时；跳过和进行中的尝试不计入。<br />
      p50 {ms(value?.p50_ms)} · p95 {ms(value?.p95_ms)} · 最近 {ms(value?.last_ms)}<br />
      {count(samples)} 次耗时样本 · 分位值为直方图上界估计
      {!recorded ? <><br />当前范围未记录总时间，不从首字延迟或成功率推算。</> : attempts > samples && <><br />部分尝试未记录耗时或历史摘要尚未补齐，仅统计已有耗时样本。</>}
    </>}>
      <div className="dual-metric" aria-label="渠道总时间">
        {recorded ? <>
          <span className="mono" aria-label={`总时间 p50 ${ms(value?.p50_ms)}`}>{ms(value?.p50_ms)}</span>
          <span className="muted mono" aria-label={`总时间 p95 ${ms(value?.p95_ms)}`}>{ms(value?.p95_ms)}</span>
        </> : <span className="muted">未记录</span>}
      </div>
    </Tip>
  );
}
export function ChannelMetricHeaders({
  keySelected = false,
  channels = [],
  layout = "all",
  compact = false,
  columns = observationPreset(layout, compact),
  renderDetection,
}: MetricColumnsProps & {
  keySelected?: boolean;
  channels?: Channel[];
}) {
  function header(id: ObservationColumn) {
    switch (id) {
      case "status": return <th>路由状态</th>;
      case "inflight": return <th className="column-run observation-inflight">
        <Tip text="当前正在请求此渠道的全部 API key 的尝试数，不受历史时间窗口或 API key 用量筛选影响。">
          {keySelected ? "渠道总并发" : "当前并发"} <CircleHelp size={12} />
        </Tip>
      </th>;
      case "success": return <th className="column-run column-success">
        <Tip text={<FailureCounts channels={channels} summary />}>
          尝试成功率
        </Tip>
      </th>;
      case "attempts": return <th className="column-quality">尝试数</th>;
      case "wait": return <th className="column-quality">
        <Tip text="请求进入 uni-api → 渠道 HTTP 发起前。包含前序重试耗时，悬停或展开查看详情。">
          请求前等待 <small>p50</small>
        </Tip>
      </th>;
      case "latency": return <th className="column-run">首字 <small>p50 / p95</small></th>;
      case "duration": return <th className="column-quality">
        <Tip text="当前渠道单次尝试从开始到离开的总时间，包含成功、失败和取消；流式请求计到流结束。不包含前序重试和后续渠道耗时，跳过及进行中的尝试不计入。p50 / p95 为直方图上界估计。">
          总时间 <small>p50 / p95</small>
        </Tip>
      </th>;
      case "usage": return <th className="column-finance">Token / 缓存率</th>;
      case "estimated": return <th className="column-finance">估算消费</th>;
      case "actual": return <th className="column-finance">
        <Tip text="已关联账单按上游响应标识逐请求归属，跟随来源、调用 API key、渠道、模型、时间、端点和流式筛选。重试单独关联且每条账单只计一次；账单未完整匹配时显示已核对消费下界及利润上限。未接入关联的渠道按日金额仅作参考。">
          {keySelected ? "渠道实际消费" : "实际消费"} <CircleHelp size={12} />
        </Tip>
      </th>;
      case "profit": return <th className="column-finance">
        <Tip text="沿用经营参考公式：估算消费 × 售卖比例 × 6.9 − 渠道实际消费数值。成本未进行币种换算，不是财务利润，不能用于对账。覆盖不足时按现有记录标记暂计或上限。">
          利润参考 <CircleHelp size={12} />
        </Tip>
      </th>;
      case "balance": return <th className="column-balance">余额<span className="balance-heading-extra"> / 额度</span></th>;
      default: return renderDetection?.(id);
    }
  }
  return columns.map(id => <Fragment key={id}>{header(id)}</Fragment>);
}
export function ChannelMetricCells({
  row,
  inflight,
  balance,
  actualRange,
  spend,
  importedChannel = false,
  stale = false,
  metricsUnavailable = false,
  layout = "all",
  compact = false,
  columns = observationPreset(layout, compact),
  renderDetection,
}: MetricColumnsProps & {
  row: Channel;
  inflight?: number | null;
  balance?: BalanceResult;
  actualRange: ActualCostRange;
  spend?: SubChannelSpendResult;
  importedChannel?: boolean;
  stale?: boolean;
  metricsUnavailable?: boolean;
}) {
  function cell(id: ObservationColumn) {
    switch (id) {
      case "status": return <td className="observation-status"><Status row={row} /></td>;
      case "inflight": return <td className="mono column-run observation-inflight">{inflight == null ? "—" : inflight}</td>;
      case "success": {
        const measured = row.stats?.success_rate;
        const success = measured != null && measured >= 0 && measured <= 1 ? measured : null;
        const tone = success == null ? undefined : success >= 0.95 ? "healthy" : success >= 0.8 ? "warning" : "critical";
        return <td className="column-run column-success">
        <Tip keyboard={false} text={<FailureCounts channels={[row]} summary />}>
          <div className="success-cell">
            <span
              className={`mono ${success == null ? "muted" : success < 0.5 ? "negative" : ""}`}
            >
              {success == null ? <span className="observation-no-sample">{row.history_configured === false ? "未接入" : stale ? "未同步" : metricsUnavailable ? "未取得" : "无样本"}</span> : rate(success)}
            </span>
            {!!row.stats?.success_rate_denominator && <small>{count(row.stats.success || 0)}/{count(row.stats.success_rate_denominator)}</small>}
            {success != null && <span className="rate-track" data-tone={tone} aria-hidden="true">
              <span style={{ width: `${success * 100}%` }} />
            </span>}
          </div>
        </Tip>
      </td>;
      }
      case "attempts": return <td className="mono column-quality">
        {metricsUnavailable
          ? "—"
          : row.history_configured === false
            ? "未接入"
            : stale && !row.stats?.success_rate_denominator
              ? "未同步"
              : count(row.stats?.success_rate_denominator || 0)}
      </td>;
      case "wait": return <td className="column-quality">
        <Timing value={row.stats?.request_to_dispatch} wait />
      </td>;
      case "latency": {
        const latency = channelLatency(row.stats);
        return <td className="column-run">
        <div className="dual-metric">
          <Timing
            value={latency.value}
            kind={latency.kind}
            text={row.stats?.first_text}
          />
          {!!latency.value?.sample_count && <span className="muted mono">
            {ms(latency.value?.p95_ms)}
          </span>}
        </div>
      </td>;
      }
      case "duration": return <td className="column-quality">
        <TotalTiming value={row.stats?.duration} attempts={row.stats?.started} />
      </td>;
      case "usage": return <td className="mono column-finance">
        {row.stats?.usage_samples
          ? count(
              (row.stats.input_tokens || 0) + (row.stats.output_tokens || 0),
            )
          : "—"}
        <small className="usage-cache">{rate(row.stats?.cache_rate)}</small>
      </td>;
      case "estimated": return <td className="mono column-finance">{usd(row.stats?.estimated_cost_usd)}</td>;
      case "actual": return <td className="mono column-finance">
        {spend || importedChannel ? (
          <SubChannelSpendValue query={spend} />
        ) : !actualRange.supported ? (
          <Tip keyboard={false} text="此渠道未关联 sub2api 账号的业务 Key，只能读取上游按日汇总；当前滚动窗口不显示整日金额。">
            <span className="muted">按日</span>
          </Tip>
        ) : balance?.isPending && !balance.data ? (
          <span className="muted">查询中</span>
        ) : balance?.data?.actual_cost_usd == null ? (
          <span className="muted">—</span>
        ) : (
          usd(balance.data.actual_cost_usd)
        )}
      </td>;
      case "profit": {
        const profitCost = channelProfitCost(spend?.data);
        return <td className="column-finance">
        <ChannelProfit
          estimated={row.stats?.estimated_cost_usd}
          actual={profitCost?.actual}
          upperBound={profitCost?.upperBound}
          provisional={profitCost?.provisional}
          salePercent={salePercent({
            model: row.model,
            sale_percent: row.stats?.sale_percent,
          })}
        />
      </td>;
      }
      case "balance": return <td className="column-balance">
        <BalanceValue
          balance={balance?.data}
          loading={balance?.isPending}
          failed={balance?.isError}
        />
      </td>;
      default: return renderDetection?.(id);
    }
  }
  return columns.map(id => <Fragment key={id}>{cell(id)}</Fragment>);
}
