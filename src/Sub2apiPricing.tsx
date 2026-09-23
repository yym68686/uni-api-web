import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { analyticsRequest, initializationRetryInterval } from "./api";
import type { ModelPrice } from "./types";
import type { SubModelCheck } from "./sub2apiResults";
import type { Probe } from "./Sub2apiChecks";
import { assessPrice, dollars, pricePair, referencePrice } from "./sub2apiPriceCheck";
import { Spinner, Tip } from "./ui";

export function useSubPrices(user: string) {
  return useQuery({
    queryKey: ["sub2api-prices", user],
    queryFn: ({ signal }) => analyticsRequest<{ data: ModelPrice[] }>({ base: "", key: "", session: user, account: true }, "/analytics/v1/prices", signal),
    retry: false,
    refetchInterval: query => initializationRetryInterval(query) || 15000,
  });
}

export function PriceStatus({ check, prices }: { check: SubModelCheck; prices?: ModelPrice[] }) {
  const result = assessPrice(check, prices);
  const actual = result.usage;
  return <div className="sub-price-status">
    <Tip text={result.expected ? `价格设置：${pricePair(result.expected.input, result.expected.output)}${!result.expected.verified ? "（参考价未确认）" : ""} · 倍率前 · 美元／百万 token${result.comparison === "match" ? " · 与定价匹配" : result.comparison === "mismatch" ? " · 与定价不匹配" : " · 样本不足，无法比较"}${actual?.message ? ` · ${actual.message}` : ""}` : actual?.message || "尚无可确认的请求账单和参考价格"}>
      <span className={`check-status ${result.status === "abnormal" ? "fail" : result.status === "normal" ? "pass" : "inconclusive"}`}>
        {result.status === "pending" && <Spinner small />}{result.label}
      </span>
    </Tip>
    {actual?.status === "matched" && (actual.input_price != null || actual.output_price != null) && <small className={`mono ${result.status === "abnormal" ? "negative" : "muted"}`}>{pricePair(actual.input_price, actual.output_price)}</small>}
  </div>;
}

export function GroupPriceStatus({ checks, prices }: { checks: SubModelCheck[]; prices?: ModelPrice[] }) {
  const abnormal = checks.filter(check => assessPrice(check, prices).status === "abnormal");
  if (abnormal.length) return <div className="sub-price-group">{abnormal.map(check => <div key={check.model}><small className="check-source">{check.model}</small><PriceStatus check={check} prices={prices} /></div>)}</div>;
  const confirmed = checks.filter(check => assessPrice(check, prices).status === "normal").length;
  const pending = checks.some(check => assessPrice(check, prices).status === "pending");
  return <span className="muted">{pending ? "核验中" : confirmed ? `${confirmed}/${checks.length} 已确认` : "未确认"}</span>;
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return <tr><th scope="row">{label}</th><td>{children}</td></tr>;
}
export function UsageDetailRows({ probe, label, prices, model }: { probe?: Probe; label: string; prices?: ModelPrice[]; model: string }) {
  const usage = probe?.usage;
  const expected = referencePrice(model, prices);
  const unit = <small className="check-source">美元／百万 token</small>;
  const samples = (input?: number | null, output?: number | null) => `${input?.toLocaleString() ?? "—"} / ${output?.toLocaleString() ?? "—"}`;
  return <>
    <Row label={`${label}账单核验`}>{usage?.status === "matched" ? "已匹配请求账单" : usage?.status === "pending" ? "核验中，等待站点账单入库" : usage?.message || "暂无核验记录，重新检测后查询"}</Row>
    <Row label={`${label}实际扣费`}><span className="mono">{dollars(usage?.actual_cost)}</span></Row>
    <Row label={`${label}倍率前单价（输入 / 输出）`}><span className="mono">{pricePair(usage?.input_price, usage?.output_price)}</span>{unit}</Row>
    <Row label={`${label}实付单价（输入 / 输出）`}><span className="mono">{pricePair(usage?.paid_input_price, usage?.paid_output_price)}</span>{unit}</Row>
    <Row label={`${label}价格设置（输入 / 输出）`}><span className="mono">{pricePair(expected?.input, expected?.output)}</span>{unit}</Row>
    <Row label={`${label}计费倍率`}>{usage?.rate_multiplier ?? "—"}</Row>
    <Row label={`${label}token（输入 / 输出）`}>{samples(usage?.input_tokens, usage?.output_tokens)}</Row>
    <Row label={`${label}缓存 token（读取 / 写入）`}>{samples(usage?.cache_read_tokens, usage?.cache_creation_tokens)}</Row>
    <Row label={`${label}请求标识`}><span className="mono">{usage?.request_id || probe?.id || "—"}</span></Row>
    {usage?.log_id && <Row label={`${label}账单 ID`}>{usage.log_id}</Row>}
    {usage?.message && usage.status === "matched" && <Row label={`${label}计费说明`}>{usage.message}</Row>}
  </>;
}
