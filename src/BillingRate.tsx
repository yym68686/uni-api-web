import type { SubTarget } from "./Sub2apiChecks";
import type { ManagedChannel } from "./channelManagement";
import { billingRate } from "./billingRates";
import { dateTime } from "./format";
import { CircleAlert } from "./icons";
import { Tip } from "./ui";

export function BillingRate({
  target,
  model = "",
  configured,
  compact = false,
}: {
  target: SubTarget;
  model?: string;
  configured?: ManagedChannel;
  compact?: boolean;
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
    ? `检测令牌账单的实际费用 ÷ 倍率前费用。${model ? `模型：${model}。` : "各模型最近检测账单；多倍率时显示范围，筛选和排序取最高值。"}${billing.observedAt ? `账单时间：${dateTime(billing.observedAt)}。` : ""}这是历史检测样本，不代表当前业务令牌的倍率。`
    : "尚无对应模型的有效检测账单，公布倍率尚未核对。";
  const explanation = details + (billing.coefficient !== 1 ? `已乘站点倍率系数 ${number(billing.coefficient)}。` : "");
  const note = billing.mismatch ? `历史检测与公布不一致 · 公布 ${number(billing.advertised!)}`
    : billing.observed ? "历史检测账单" : billing.rate != null ? "公布 · 待核对" : "暂无可确认倍率";
  if (compact) return <Tip text={<><strong>{note}</strong><div>{explanation}</div></>}>
    <span className="mono management-metric" title={note}>{value}
      {billing.mismatch && <CircleAlert className="negative" aria-label="倍率不一致" size={12} />}
    </span>
  </Tip>;
  return (
    <div className="mono" title={explanation}>
      <span>{value}</span>
      {billing.mismatch ? (
        <small className="check-source check-status fail">
          {note}
        </small>
      ) : (
        <small className="check-source">
          {billing.observed
            ? "历史检测账单"
            : billing.rate != null
              ? "公布 · 待核对"
              : "待核对"}
        </small>
      )}
    </div>
  );
}
