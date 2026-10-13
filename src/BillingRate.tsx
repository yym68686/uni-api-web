import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";
import { billingRate } from "./billingRates";
import { time } from "./format";

export function BillingRate({
  target,
  model = "",
  configured,
}: {
  target: SubTarget;
  model?: string;
  configured?: ManagedChannel;
}) {
  const billing = billingRate(target, model, configured);
  const number = (value: number) =>
    value.toLocaleString("en-US", { maximumFractionDigits: 8 });
  const value =
    billing.rate == null
      ? "—"
      : billing.minimum !== billing.rate && billing.minimum != null
        ? `${number(billing.minimum)}–${number(billing.rate)}`
        : number(billing.rate);
  const details = billing.observed
    ? `已核对账单的实际费用 ÷ 倍率前费用。${model ? `模型：${model}。` : "各模型最近账单；多倍率时显示范围，筛选和排序取最高值。"}${billing.observedAt ? `账单时间：${time(billing.observedAt)}。` : ""}历史样本不保证后续请求倍率不变。`
    : "尚无对应模型的有效账单，公布倍率尚未核对。";
  return (
    <div className="mono" title={details}>
      <span>{value}</span>
      {billing.mismatch ? (
        <small className="check-source check-status fail">
          倍率不一致 · 公布 {number(billing.advertised!)}
        </small>
      ) : (
        <small className="check-source">
          {billing.observed
            ? "账单已核对"
            : billing.rate != null
              ? "公布 · 待核对"
              : "待核对"}
        </small>
      )}
    </div>
  );
}
