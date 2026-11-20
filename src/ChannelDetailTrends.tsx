import { channelLatency } from "./channelLatency";
import { DetailLoading } from "./PageLoading";
import { FailureCounts } from "./FailureCounts";
import { useMemo, useState } from "react";
import { AnalyticsInitializingError } from "./api";
import { ranges } from "./analytics";
import { count, ms, rate } from "./format";
import { useChannelTimeseries } from "./channelTimeseries";
import type { ChannelTimeseriesProps } from "./channelTimeseries";
import { TrendDataTable, TrendViewSwitch, type TrendMode } from "./TrendView";
import type { Channel } from "./types";
import "./channelDetail.css";

type Point = NonNullable<Channel["points"]>[number];
const date = (at: number) =>
  new Date(at * 1000).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
const successRate = (point: Point) =>
  point.success + point.failed > 0
    ? point.success / (point.success + point.failed)
    : null;
const timeSpan = (p: Point) =>
  p.bucket_end && p.bucket_end !== p.timestamp
    ? `${date(p.timestamp)}–${date(p.bucket_end)}`
    : date(p.timestamp);
interface Series {
  label: string;
  value: (point: Point) => number | null;
  color: string;
}
const latencySeries: Series[] = [
  {
    label: "p50",
    value: (p) => channelLatency(p)?.p50_ms ?? null,
    color: "var(--green)",
  },
  {
    label: "p95",
    value: (p) => channelLatency(p)?.p95_ms ?? null,
    color: "var(--danger)",
  },
];
const successSeries: Series[] = [
  { label: "成功率", value: successRate, color: "var(--green)" },
];
const cacheSeries: Series[] = [
  {
    label: "缓存率",
    value: (p) => p.cache_rate ?? null,
    color: "var(--accent)",
  },
];
const latencyDetail = (p: Point) => {
  const latency = channelLatency(p);
  return `${count(latency?.sample_count || 0)} 个成功尝试首字样本 · 正文首字 p50 ${ms(p.first_text?.p50_ms)}`;
};
const successDetail = (p: Point) =>
  `成功 ${count(p.success || 0)} / 失败 ${count(p.failed || 0)} · ${count((p.success || 0) + (p.failed || 0))} 次有效尝试`;
const cacheDetail = (p: Point) =>
  `缓存读取 ${count(p.cache_read_tokens || 0)} / 输入 ${count(p.input_tokens || 0)} token · ${count(p.cache_samples || 0)} 个缓存样本`;

function Curve({
  title,
  points,
  series,
  from,
  to,
  bucket,
  detail,
  empty,
  percentage = false,
}: {
  title: string;
  points: Point[];
  series: Series[];
  from: number;
  to: number;
  bucket: number;
  detail: (point: Point) => string;
  empty?: string;
  percentage?: boolean;
}) {
  const [active, setActive] = useState<number | null>(null);
  const known = points.filter((p) => series.some((s) => s.value(p) != null));
  const selected = known.find((p) => p.timestamp === active) || known.at(-1);
  const max = percentage
    ? 1
    : Math.max(1, ...known.flatMap((p) => series.map((s) => s.value(p) || 0)));
  const x = (at: number) => 60 + ((at - from) / Math.max(1, to - from)) * 342;
  const hitWidth = Math.min(12, (bucket / Math.max(1, to - from)) * 342);
  const y = (value: number) =>
    14 + (1 - Math.max(0, Math.min(1, value / max))) * 126;
  const format = percentage ? rate : ms;
  const label = (p: Point) =>
    `${timeSpan(p)} · ${series.map((s) => `${s.label} ${format(s.value(p))}`).join(" / ")} · ${detail(p)}${p.covered === false ? " · 覆盖不完整" : ""}`;
  return (
    <section className="detail-curve" aria-label={`${title}趋势`}>
      <div className="detail-curve-heading">
        <h4>{title}</h4>
        <div className="detail-curve-legend">
          {series.map((s) => (
            <span key={s.label}>
              <i style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
        </div>
      </div>
      {!known.length ? (
        <p className="detail-curve-empty muted">
          {empty || `当前范围暂无${title}样本。`}
        </p>
      ) : (
        <>
          <svg
            viewBox="0 0 420 174"
            role="group"
            tabIndex={0}
            aria-label={`${title}曲线，使用左右方向键查看数据点`}
            onKeyDown={(event) => {
              const index = selected ? known.indexOf(selected) : 0;
              const target =
                event.key === "ArrowLeft"
                  ? index - 1
                  : event.key === "ArrowRight"
                    ? index + 1
                    : event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? known.length - 1
                        : null;
              if (target === null) return;
              event.preventDefault();
              setActive(
                known[Math.max(0, Math.min(target, known.length - 1))]
                  .timestamp,
              );
            }}
          >
            {[0, 0.5, 1].map((tick) => (
              <g key={tick}>
                <line x1="60" x2="402" y1={y(max * tick)} y2={y(max * tick)} />
                <text x="52" y={y(max * tick) + 4} textAnchor="end">
                  {format(max * tick)}
                </text>
              </g>
            ))}
            {selected && (
              <line
                className="detail-curve-guide"
                x1={x(selected.timestamp)}
                x2={x(selected.timestamp)}
                y1="14"
                y2="140"
              />
            )}
            {series.map((s) => {
              let path = "",
                previous: Point | undefined;
              for (const p of points) {
                const value = s.value(p);
                if (value == null) {
                  previous = undefined;
                  continue;
                }
                const connected =
                  previous && p.timestamp - previous.timestamp <= bucket * 1.01;
                path += `${connected ? "L" : "M"}${x(p.timestamp)},${y(value)} `;
                previous = p;
              }
              return (
                <g key={s.label} style={{ color: s.color }}>
                  <path
                    d={path}
                    className="detail-curve-line"
                    data-series={s.label}
                  />
                  {known
                    .filter((p) => s.value(p) != null)
                    .map((p) => (
                      <circle
                        key={p.timestamp}
                        cx={x(p.timestamp)}
                        cy={y(s.value(p)!)}
                        r={selected?.timestamp === p.timestamp ? 3.5 : 2}
                        aria-hidden="true"
                      />
                    ))}
                </g>
              );
            })}
            {known.map((p) => (
              <rect
                key={p.timestamp}
                className="detail-curve-hit"
                x={x(p.timestamp) - hitWidth / 2}
                y="10"
                width={hitWidth}
                height="134"
                role="img"
                aria-label={label(p)}
                onMouseEnter={() => setActive(p.timestamp)}
                onClick={() => setActive(p.timestamp)}
              >
                <title>{label(p)}</title>
              </rect>
            ))}
            <text x="60" y="165">
              {date(from)}
            </text>
            <text x="402" y="165" textAnchor="end">
              {date(to)}
            </text>
          </svg>
          {selected && (
            <div className="detail-curve-value" aria-live="polite">
              <span>{timeSpan(selected)}</span>
              <strong>
                {series
                  .map((s) => `${s.label} ${format(s.value(selected))}`)
                  .join(" / ")}
              </strong>
              <small>
                {detail(selected)}
                {selected.covered === false ? " · 覆盖不完整" : ""}
              </small>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function ChannelTrendTable({ points }: { points: Point[] }) {
  return (
    <TrendDataTable
      label="渠道分时数据"
      columns={["时间段", "首字延迟", "成功率", "缓存率"]}
    >
      {points.map((p) => {
        const latency = channelLatency(p);
        const success = successRate(p);
        return (
          <tr key={p.timestamp}>
            <th scope="row">
              <span className="trend-reading">{date(p.timestamp)}</span>
              {!!p.bucket_end && (
                <small className="trend-reading">至 {date(p.bucket_end)}</small>
              )}
              {p.covered === false && (
                <small className="trend-partial">覆盖不完整</small>
              )}
            </th>
            <td>
              {latency?.p50_ms == null && latency?.p95_ms == null ? (
                <span className="muted">未记录</span>
              ) : (
                <>
                  <span className="trend-reading">
                    p50 {ms(latency.p50_ms)}
                  </span>
                  <span className="trend-reading">
                    p95 {ms(latency.p95_ms)}
                  </span>
                </>
              )}
              {(!!latency?.sample_count || p.first_text?.p50_ms != null) && (
                <small>{latencyDetail(p)}</small>
              )}
            </td>
            <td>
              {success == null ? (
                <span className="muted">未记录</span>
              ) : (
                <>
                  <span className="trend-reading">{rate(success)}</span>
                  <small>{successDetail(p)}</small>
                </>
              )}
            </td>
            <td>
              {p.cache_rate == null ? (
                <span className="muted">未记录</span>
              ) : (
                <>
                  <span className="trend-reading">{rate(p.cache_rate)}</span>
                  <small>{cacheDetail(p)}</small>
                </>
              )}
            </td>
          </tr>
        );
      })}
    </TrendDataTable>
  );
}

export function ChannelDetailTrends(props: ChannelTimeseriesProps) {
  const query = useChannelTimeseries(props);
  const [mode, setMode] = useState<TrendMode>("chart");
  // The server bounds this aggregate series to ~180 buckets. Keep the full
  // selected range rather than silently showing only the last 120 buckets.
  const points = useMemo(
    () =>
      [...(query.data?.data.flatMap((c) => c.points || []) || [])].sort(
        (a, b) => a.timestamp - b.timestamp,
      ),
    [query.data],
  );
  const from = Math.max(
    query.data?.from || 0,
    query.data?.collection_started_at || 0,
  );
  const to = Math.max(from + 1, query.data?.to || 0);
  const bucket = query.data?.bucket_seconds || 60;
  const ready = !query.isPending && !query.isError;
  const cacheRate = query.data?.total?.cache_rate;
  return (
    <section
      className="detail-section channel-detail-trends"
      aria-label="渠道趋势曲线"
    >
      <header className="detail-trends-heading">
        <div>
          <h3>渠道趋势</h3>
          <p className="muted">
            {ranges.find(([v]) => v === props.window)?.[1] || props.window} ·
            跟随 API key、端点和流式筛选
          </p>
        </div>
        {ready && points.length > 0 && (
          <TrendViewSwitch value={mode} onChange={setMode} />
        )}
      </header>
      {query.isPending || query.error instanceof AnalyticsInitializingError ? (
        <DetailLoading kind="chart" label="渠道趋势" />
      ) : query.isError ? (
        <p role="alert">
          趋势读取失败{" "}
          <button className="button small" onClick={() => void query.refetch()}>
            重试
          </button>
        </p>
      ) : (
        <>
          <div
            className="detail-curves"
            hidden={mode === "table" && points.length > 0}
          >
            <Curve
              title="首字延迟"
              points={points}
              series={latencySeries}
              from={from}
              to={to}
              bucket={bucket}
              detail={latencyDetail}
            />
            <Curve
              title="成功率"
              points={points}
              series={successSeries}
              from={from}
              to={to}
              bucket={bucket}
              detail={successDetail}
              percentage
            />
            <Curve
              title="缓存率"
              points={points}
              series={cacheSeries}
              from={from}
              to={to}
              bucket={bucket}
              detail={cacheDetail}
              percentage
              empty="此时间范围暂无缓存用量数据。"
            />
          </div>
          {mode === "table" && points.length > 0 && (
            <ChannelTrendTable points={points} />
          )}
          {cacheRate != null && (
            <p className="detail-trends-cache muted">
              <span>所选范围缓存率</span> <strong>{rate(cacheRate)}</strong> ·
              缓存读取 token ÷ 输入 token
            </p>
          )}
          <FailureCounts channels={query.data?.data || []} />
        </>
      )}
    </section>
  );
}
