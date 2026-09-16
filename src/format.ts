import type { Balance, BalanceKey, Channel } from "./types";
export const count = (value: number) =>
  new Intl.NumberFormat("en-US").format(value);
export const ms = (value?: number | null) =>
  value == null
    ? "—"
    : value < 1000
      ? `${Math.round(value)} ms`
      : `${(value / 1000).toFixed(2)} s`;
export const rate = (value?: number | null) =>
  value == null ? "—" : `${(value * 100).toFixed(1)}%`;
export const time = (value?: number | null) =>
  value
    ? new Date(value * 1000).toLocaleTimeString("zh-CN", { hour12: false })
    : "暂无";
export const rowId = (row: Channel) =>
  JSON.stringify([
    row.source_id || "",
    row.provider,
    row.model,
    row.upstream_model,
    row.endpoint,
    row.stream,
  ]);
export const balanceStatus: Record<string, string> = {
  unsupported: "未适配",
  no_key: "无密钥",
  access_denied: "鉴权失败",
  timeout: "查询超时",
  network_error: "连接失败",
  busy: "查询繁忙",
  rate_limited: "上游限流",
  redirect_blocked: "上游重定向",
  upstream_error: "上游异常",
  response_too_large: "响应过大",
  unsupported_credential: "密钥未适配",
  proxy_error: "代理异常",
  query_error: "查询失败",
};
const nonPositive = (value: unknown) =>
  typeof value === "number" && Number.isFinite(value) && value <= 0;
export function keyIsLow(key: BalanceKey) {
  return (
    key.status === "ok" &&
    !key.unlimited &&
    (nonPositive(key.amount) ||
      key.windows?.some((w) => nonPositive(w.remaining)))
  );
}
export function balanceIsLow(balance?: Balance) {
  return !!(
    balance?.status === "complete" &&
    balance.keys?.length &&
    balance.keys.length === balance.key_count &&
    !balance.omitted_keys &&
    balance.keys.every(keyIsLow)
  );
}
export function balanceLabel(key: BalanceKey) {
  if (key.status !== "ok") return balanceStatus[key.status] || "查询失败";
  if (key.unlimited) return "无限额";
  if (typeof key.amount === "number" && Number.isFinite(key.amount))
    return `${key.currency === "USD" ? "$" : `${key.currency || ""} `}${key.amount.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
  if (key.windows?.length)
    return key.windows
      .map(
        (w) =>
          `${w.window} · ${key.currency || "USD"} ${w.remaining.toFixed(2)}`,
      )
      .join(" / ");
  return "上游未提供";
}
export const balanceKind: Record<string, string> = {
  wallet: "钱包余额",
  key_quota: "Key 配额",
  key_rate_limits: "周期额度",
  subscription: "订阅剩余",
};
export const reasonLabel: Record<string, string> = {
  eligible: "可用",
  channel_cooldown: "冷却中",
  no_provider_key: "无密钥",
};
export function summarize(rows: Channel[]) {
  const providers = new Set(rows.map((row) => row.provider));
  const eligible = new Set(
    rows.filter((row) => row.eligible).map((row) => row.provider),
  );
  const completed = rows.reduce(
    (sum, row) => sum + (row.stats?.success_rate_denominator || 0),
    0,
  );
  const success = rows.reduce((sum, row) => sum + (row.stats?.success || 0), 0);
  return {
    providers: providers.size,
    eligible: eligible.size,
    completed,
    success,
    successRate: completed ? success / completed : null,
  };
}

export const providerId = (row: Pick<Channel,"source_id"|"provider">) => JSON.stringify([row.source_id || "",row.provider]);
