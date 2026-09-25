import { useMemo, useState } from "react";
import { AnalyticsInitializingError } from "./api";
import { ranges } from "./analytics";
import { ms, rate } from "./format";
import { Spinner } from "./ui";
import { useChannelTimeseries } from "./channelTimeseries";
import type { ChannelTimeseriesProps } from "./channelTimeseries";
import type { Channel } from "./types";

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
interface Series {
  label: string;
  value: (point: Point) => number | null;
  color: string;
}
const latencySeries: Series[] = [
  {
    label: "p50",
    value: (p) => p.response_created?.p50_ms ?? null,
    color: "var(--green)",
  },
  {
    label: "p95",
    value: (p) => p.response_created?.p95_ms ?? null,
    color: "var(--danger)",
  },
];
const successSeries: Series[] = [
  { label: "成功率", value: successRate, color: "var(--green)" },
];

function Curve({
  title,
  points,
  series,
  from,
  to,
  bucket,
  percentage = false,
}: {
  title: string;
  points: Point[];
  series: Series[];
  from: number;
  to: number;
  bucket: number;
  percentage?: boolean;
}) {
  const [active, setActive] = useState<number | null>(null);
  const known = points.filter((p) => series.some((s) => s.value(p) != null));
  const selected = known.find((p) => p.timestamp === active) || known.at(-1);
  const max = percentage
    ? 1
    : Math.max(1, ...known.flatMap((p) => series.map((s) => s.value(p) || 0)));
  const x = (at: number) => 60 + ((at - from) / Math.max(1, to - from)) * 342;
  const y = (value: number) =>
    14 + (1 - Math.max(0, Math.min(1, value / max))) * 126;
  const format = percentage ? rate : ms;
  const detail = (p: Point) =>
    percentage
      ? `成功 ${p.success} / 失败 ${p.failed} · ${p.success + p.failed} 次有效尝试`
      : `${p.response_created?.sample_count || 0} 个响应创建样本 · 正文首字 p50 ${ms(p.first_text?.p50_ms)}`;
  const label = (p: Point) =>
    `${date(p.timestamp)}–${date(p.bucket_end || p.timestamp)} · ${series.map((s) => `${s.label} ${format(s.value(p))}`).join(" / ")} · ${detail(p)}${p.covered === false ? " · 覆盖不完整" : ""}`;
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
        <p className="muted">当前范围暂无{title}样本。</p>
      ) : (
        <>
          <svg
            viewBox="0 0 420 174"
            role="group"
            aria-label={`${title}曲线，聚焦数据点查看详情`}
          >
            {[0, 0.5, 1].map((tick) => (
              <g key={tick}>
                <line x1="60" x2="402" y1={y(max * tick)} y2={y(max * tick)} />
                <text x="52" y={y(max * tick) + 4} textAnchor="end">
                  {format(max * tick)}
                </text>
              </g>
            ))}
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
                x={x(p.timestamp) - 4}
                y="10"
                width="8"
                height="134"
                tabIndex={0}
                role="img"
                aria-label={label(p)}
                onMouseEnter={() => setActive(p.timestamp)}
                onFocus={() => setActive(p.timestamp)}
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
              <span>{date(selected.timestamp)}</span>
              <strong>
                {series
                  .map((s) => `${s.label} ${format(s.value(selected))}`)
                  .join(" / ")}
              </strong>
              <small>{detail(selected)}</small>
            </div>
          )}
        </>
      )}
    </section>
  );
}

export function ChannelDetailTrends(props: ChannelTimeseriesProps) {
  const query = useChannelTimeseries(props);
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
  return (
    <section
      className="detail-section channel-detail-trends"
      aria-label="渠道趋势曲线"
    >
      <h3>
        渠道趋势曲线{" "}
        <span className="muted">
          {ranges.find(([v]) => v === props.window)?.[1] || props.window}
        </span>
      </h3>
      <p className="muted">
        当前来源、渠道与模型 · 跟随 API key、时间、端点和流式筛选
      </p>
      {query.isPending ? (
        <p role="status">
          <Spinner small /> 正在读取渠道趋势…
        </p>
      ) : query.error instanceof AnalyticsInitializingError ? (
        <p role="status">
          <Spinner small /> {query.error.message}
        </p>
      ) : query.isError ? (
        <p role="alert">
          趋势读取失败{" "}
          <button className="button small" onClick={() => void query.refetch()}>
            重试
          </button>
        </p>
      ) : (
        <>
          <Curve
            title="首字延迟"
            points={points}
            series={latencySeries}
            from={from}
            to={to}
            bucket={query.data?.bucket_seconds || 60}
          />
          <Curve
            title="成功率"
            points={points}
            series={successSeries}
            from={from}
            to={to}
            bucket={query.data?.bucket_seconds || 60}
            percentage
          />
          <p className="muted">
            首字延迟沿用列表口径：请求发起至响应创建，p50 / p95
            为直方图估计。成功率 = 成功 ÷（成功 +
            失败）；取消和跳过不计入分母，无样本区间留空。
          </p>
        </>
      )}
    </section>
  );
}
