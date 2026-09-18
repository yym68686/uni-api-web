import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { channelParams } from "./api";
import { readMetrics } from "./metricsApi";
import { count, rate } from "./format";
import { ranges } from "./analytics";
import { Spinner } from "./ui";
import type { Channel, Connection } from "./types";

export interface CacheTrendProps {
  row: Channel;
  connection: Connection;
  keyId: string;
  window: string;
  endpoint: string;
  stream: string;
  refresh: number;
}
export function CacheTrend({ row, connection, keyId, window, endpoint, stream, refresh }: CacheTrendProps) {
  const [active, setActive] = useState<number | null>(null);
  const query = useQuery({
    queryKey: ["cache-trend", connection.session, row.source_id, row.provider, row.model, row.upstream_model, keyId, window, endpoint, stream, refresh],
    queryFn: ({ signal }) => {
      const params = channelParams(keyId, window, row.model, endpoint, stream);
      params.set("provider", row.provider);
      params.set("upstream_model", row.upstream_model);
      return readMetrics({ ...connection, sourceId: row.source_id || connection.sourceId }, "/v1/channel-metrics/timeseries?" + params, signal, endpoint, stream);
    },
    retry: false,
    staleTime: 15000,
  });
  const data = query.data;
  const points = data?.data.flatMap((channel) => channel.points || []) || [];
  const known = points.filter((p) => p.cache_rate != null);
  const from = Math.max(data?.from || 0, data?.collection_started_at || 0);
  const to = data?.to || from + 1;
  const x = (at: number) => 38 + ((at - from) / Math.max(1, to - from)) * 356;
  const y = (value: number) => 14 + (1 - Math.max(0, Math.min(1, value))) * 140;
  let path = "";
  let previous: typeof points[number] | undefined;
  for (const point of points) {
    if (point.cache_rate == null) { previous = undefined; continue; }
    const connected = previous && point.timestamp - previous.timestamp <= (data?.bucket_seconds || 60) * 1.01;
    path += `${connected ? "L" : "M"}${x(point.timestamp)},${y(point.cache_rate)} `;
    previous = point;
  }
  const selected = known.find((point) => point.timestamp === active) || known.at(-1);
  const date = (at: number) => new Date(at * 1000).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
  const tooltip = (point: typeof points[number]) => `${date(point.timestamp)}–${date(point.bucket_end || point.timestamp)} · 缓存率 ${rate(point.cache_rate)} · 缓存读取 ${count(point.cache_read_tokens || 0)} / 输入 ${count(point.input_tokens || 0)} token · ${count(point.cache_samples || 0)} 个缓存样本`;
  return <section className="detail-section cache-trend" aria-label="历史缓存率">
    <h3>缓存率曲线 <span className="muted">{ranges.find(([value]) => value === window)?.[1] || window}</span></h3>
    <p className="muted">当前渠道与模型 · 跟随时间、API key、端点和流式筛选</p>
    {query.isPending ? <p role="status"><Spinner small /> 正在读取缓存历史…</p> : query.isError ? <p role="alert">缓存历史读取失败 <button className="button small" onClick={() => void query.refetch()}>重试</button></p> : !known.length ? <p className="muted">此时间范围暂无缓存用量数据。</p> : <>
      <div className="cache-trend-summary"><strong>{rate(data?.total?.cache_rate)}</strong><span className="muted">所选范围缓存率</span></div>
      <svg viewBox="0 0 410 185" role="group" aria-label="缓存率随时间变化，聚焦数据点查看详情">
        {[0, .5, 1].map((value) => <g key={value}><line x1="38" x2="394" y1={y(value)} y2={y(value)} /><text x="30" y={y(value) + 4} textAnchor="end">{value * 100}%</text></g>)}
        <path d={path} className="cache-trend-line" />
        {known.map((point) => <circle key={point.timestamp} cx={x(point.timestamp)} cy={y(point.cache_rate!)} r={selected?.timestamp === point.timestamp ? 4 : 2.5} tabIndex={0} role="img" aria-label={tooltip(point)} onMouseEnter={() => setActive(point.timestamp)} onFocus={() => setActive(point.timestamp)} onClick={() => setActive(point.timestamp)}><title>{tooltip(point)}</title></circle>)}
        <text x="38" y="178">{date(from)}</text><text x="394" y="178" textAnchor="end">{date(to)}</text>
      </svg>
      <div className="cache-trend-point" aria-live="polite">{selected && <><span>{date(selected.timestamp)}–{date(selected.bucket_end || selected.timestamp)}</span><strong>{rate(selected.cache_rate)}</strong><small>缓存读取 {count(selected.cache_read_tokens || 0)} / 输入 {count(selected.input_tokens || 0)} token</small></>}</div>
      <p className="muted">缓存率 = 缓存读取 token ÷ 输入 token。无样本的时间段留空。</p>
    </>}
  </section>;
}
