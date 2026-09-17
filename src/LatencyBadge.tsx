import { ms } from "./format";
export function LatencyBadge({ value }: { value?: number | null }) {
  if (value == null || !Number.isFinite(value) || value < 0)
    return <span className="muted">—</span>;
  const level = value <= 5000 ? "fast" : value <= 10000 ? "medium" : "slow";
  return <span className={`latency-badge ${level}`}>{ms(value)}</span>;
}
