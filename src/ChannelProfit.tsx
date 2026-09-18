import { Tip } from "./ui";

const known = (value: number | null | undefined): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

export function channelProfit(estimated?: number | null, actual?: number | null) {
  if (!known(estimated) || !known(actual)) return null;
  const revenue = estimated * 0.025 * 6.9;
  const amount = revenue - actual;
  if (!Number.isFinite(revenue) || !Number.isFinite(amount)) return null;
  const margin = revenue > 0 ? amount / revenue : null;
  return { amount, margin: margin != null && Number.isFinite(margin) ? margin : null };
}

export function ChannelProfit({ estimated, actual }: { estimated?: number | null; actual?: number | null }) {
  const profit = channelProfit(estimated, actual);
  const number = (value: number) => value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 6 });
  const percent = profit?.margin == null ? "—" : `${(profit.margin * 100).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;
  const explanation = profit
    ? `仅取数值：${estimated} × 2.5% × 6.9 − ${actual} = ¥${profit.amount}。利润率 = 利润 ÷（估算消费 × 2.5% × 6.9）。${estimated === 0 ? "收入为 0，利润率无法计算。" : ""}使用本行显示的消费口径，共用业务 Key 的渠道不能重复相加。`
    : "等待估算消费和渠道实际消费均可用后计算；未查询到的金额不按 0 处理。";
  return <Tip text={explanation}>
    <div className={`channel-profit mono ${profit == null ? "muted" : profit.amount < 0 ? "negative" : profit.amount > 0 ? "profit-positive" : ""}`}>
      <span>{profit ? `¥${number(profit.amount)}` : "—"}</span>
      <small aria-label={`利润率 ${percent}`}>{percent}</small>
    </div>
  </Tip>;
}
