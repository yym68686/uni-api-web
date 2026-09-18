import { ms } from "./format";
import { Tip } from "./ui";
export function LatencyBadge({ value }: { value?: number | null }) {
  if (value == null || !Number.isFinite(value) || value < 0)
    return <span className="muted">—</span>;
  const level = value <= 5000 ? "fast" : value <= 10000 ? "medium" : "slow";
  return <span className={`latency-badge ${level}`}>{ms(value)}</span>;
}

export function ResponseLatency({
  created,
  text,
}: {
  created?: number | null;
  text?: number | null;
}) {
  return (
    <Tip
      text={
        <>
          首个 response.created：{ms(created)}
          <br />
          首个 response.output_text.delta：{ms(text)}
        </>
      }
    >
      <span>
        <LatencyBadge value={created} />
      </span>
    </Tip>
  );
}
