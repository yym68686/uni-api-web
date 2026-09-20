import { Tip } from "./ui";
import type { SubChannelSpend } from "./SubChannelSpend";

const known = (value: number | null | undefined): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

// Costs already assigned to this exact caller/filter are a lower bound on the
// total cost. Subtracting that bound gives a profit upper bound, not an estimate
// that assumes the missing requests were free. Never use shared-key totals.
export function channelProfitCost(spend?: SubChannelSpend) {
  if (spend?.scope !== "matched_requests") return undefined;
  if (spend.status === "complete" && known(spend.actual_cost_usd))
    return { actual: spend.actual_cost_usd, upperBound: false };
  const reconciled = (spend.matched_attempts || 0) + (spend.confirmed_unbilled_attempts || 0);
  if (spend.status !== "complete" && reconciled > 0 && known(spend.matched_cost_usd))
    return { actual: spend.matched_cost_usd, upperBound: true };
  return undefined;
}

export function channelProfit(
  estimated?: number | null,
  actual?: number | null,
  salePercent = 2.5,
) {
  if (!known(estimated) || !known(actual) || !known(salePercent)) return null;
  const revenue = estimated * (salePercent / 100) * 6.9;
  const amount = revenue - actual;
  if (!Number.isFinite(revenue) || !Number.isFinite(amount)) return null;
  const margin = revenue > 0 ? amount / revenue : null;
  return {
    amount,
    margin: margin != null && Number.isFinite(margin) ? margin : null,
  };
}

export function ChannelProfit({
  estimated,
  actual,
  salePercent = 2.5,
  upperBound = false,
}: {
  estimated?: number | null;
  actual?: number | null;
  salePercent?: number;
  upperBound?: boolean;
}) {
  const profit = channelProfit(estimated, actual, salePercent);
  const number = (value: number) =>
    value.toLocaleString("en-US", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 6,
    });
  const percent =
    profit?.margin == null
      ? "—"
      : `${(profit.margin * 100).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;
  const prefix = upperBound ? "≤" : "";
  const displayedPercent = profit?.margin == null ? "—" : prefix + percent;
  const explanation = profit
    ? `${upperBound ? "账单尚未全部核对，使用已核对费用计算利润上限。实际费用可能更高，实际利润及利润率可能更低；未核对费用没有按零处理。全部核对完成后自动显示正常利润。" : ""}仅取数值：${estimated} × ${salePercent}% × 6.9 − ${actual} = ¥${profit.amount}${upperBound ? "（上限）" : ""}。利润率 = 利润 ÷（估算消费 × ${salePercent}% × 6.9）。${estimated === 0 || salePercent === 0 ? "收入为 0，利润率无法计算。" : ""}使用本行显示的消费口径，共用业务 Key 的渠道不能重复相加。`
    : "等待估算消费和渠道实际消费均可用后计算；未查询到的金额不按 0 处理。";
  return (
    <Tip text={explanation}>
      <div
        className={`channel-profit mono ${profit == null ? "muted" : profit.amount < 0 ? "negative" : upperBound ? "profit-upper-bound" : profit.amount > 0 ? "profit-positive" : ""}`}
      >
        <span>{profit ? `${prefix}¥${number(profit.amount)}` : "—"}{profit && upperBound && <small className="profit-bound-label">上限</small>}</span>
        <small aria-label={`${upperBound ? "利润率上限" : "利润率"} ${displayedPercent}`}>{displayedPercent}</small>
      </div>
    </Tip>
  );
}
