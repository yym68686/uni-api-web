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
  protocol,
  firstResponse,
  output,
}: {
  created?: number | null;
  text?: number | null;
  protocol?: string;
  firstResponse?: number | null;
  output?: number | null;
}) {
  const native = protocol === "gemini" || protocol === "messages";
  const responseLabel =
    protocol === "gemini"
      ? "首个 Gemini 响应事件"
      : protocol === "messages"
        ? "首个 message_start"
        : "首个 response.created";
  const textLabel =
    protocol === "gemini"
      ? "首个 Gemini 非思考文本片段"
      : protocol === "messages"
        ? "首个 Messages 文本片段"
        : "首个上游正文片段";
  return (
    <Tip
      text={
        <>
          {responseLabel}：{ms(native ? firstResponse : created)}
          <br />
          {textLabel}：{ms(text)}
          {output != null && <><br />首个上游输出（含正文、推理或工具调用）：{ms(output)}</>}
        </>
      }
    >
      <span>
        <LatencyBadge value={native ? firstResponse : created ?? output} />
      </span>
    </Tip>
  );
}
