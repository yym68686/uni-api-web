import type { Channel } from "./types";

export function trafficBuckets(rows: Channel[], step: number, limit = 120) {
  const interval = Number.isFinite(step) && step > 0 ? step : 60;
  const timestamps = new Map<number, { success: number; failed: number; covered: boolean }>();
  for (const row of rows) {
    for (const point of row.points || []) {
      if (!Number.isFinite(point.timestamp)) continue;
      const entry = timestamps.get(point.timestamp);
      if (entry) {
        entry.success += point.success;
        entry.failed += point.failed;
        entry.covered &&= point.covered;
      } else {
        timestamps.set(point.timestamp, { success: point.success, failed: point.failed, covered: point.covered });
      }
    }
  }
  const sorted = [...timestamps].sort(([a], [b]) => a - b);
  const first = sorted[0]?.[0] ?? 0;
  const slots = sorted.length ? Math.floor((sorted.at(-1)![0] - first) / interval) + 1 : 0;
  const stride = Math.max(1, Math.ceil(slots / limit));
  const seconds = interval * stride;
  const buckets = Array.from({ length: Math.ceil(slots / stride) }, (_, i) => ({
    at: first + i * seconds, success: 0, failed: 0, peak: 0, covered: true, samples: 0,
  }));
  for (const [timestamp, point] of sorted) {
    const bucket = buckets[Math.floor((timestamp - first) / seconds)];
    bucket.success += point.success;
    bucket.failed += point.failed;
    bucket.peak = Math.max(bucket.peak, point.success + point.failed);
    bucket.covered &&= point.covered;
    bucket.samples++;
  }
  return {
    seconds,
    points: buckets.map((bucket, i) => ({
      ...bucket,
      covered: bucket.covered && bucket.samples === Math.min(stride, slots - i * stride),
    })),
  };
}
