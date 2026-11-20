import type { SiteWallet, Channel } from "./types";
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
export const dateTime = (seconds?: number | null) =>
  seconds && Number.isFinite(seconds) ? new Date(seconds * 1000).toLocaleString("zh-CN", { hour12: false }) : "暂无";
// Color thresholds are independent of the low wallet balance filter (<= 0).
export function balanceTone(value?: number | null) {
  if (value == null || !Number.isFinite(value)) return "muted";
  return value < 0
    ? "negative"
    : value <= 50
      ? "balance-warning"
      : "balance-positive";
}
export function balanceIsLow(wallets?: SiteWallet[]) {
  return balanceIsKnown(wallets) && wallets.every(wallet => wallet.amount <= 0);
}
export function balanceIsKnown(wallets?: SiteWallet[]): wallets is (SiteWallet & {amount: number})[] {
  return !!wallets?.length && wallets.every(wallet => wallet.status === "ok" && hasWalletAmount(wallet));
}
export function hasWalletAmount(wallet: SiteWallet): wallet is SiteWallet & {amount: number} {
  return typeof wallet.amount === "number" && Number.isFinite(wallet.amount);
}
export function walletFailureDetail(wallet: { error_reason?: string }) {
  switch (wallet.error_reason) {
    case "verification_required": return "站点要求人机验证";
    case "login_required": return "站点会话失效，请重新登录";
    case "rate_limited": return "站点限流，请稍后刷新";
    case "access_denied": return "站点拒绝访问，请检查账号权限";
    default: return "";
  }
}
export function walletStatusLabel(wallet: SiteWallet) {
  if (wallet.status === "pending") return "查询中";
  if (wallet.status === "error") {
    const status = hasWalletAmount(wallet) ? "更新失败 · 上次余额" : "站点余额查询失败";
    const detail = walletFailureDetail(wallet);
    return detail ? `${status} · ${detail}` : status;
  }
  return "暂无站点余额";
}
export function formatBalance(amount: number, currency: string) {
  return `${currency === "USD" ? "$" : `${currency} `}${amount.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}
export const reasonLabel: Record<string, string> = {
  eligible: "可用",
  channel_cooldown: "冷却中",
  temporarily_disabled: "临时停用",
  no_provider_key: "无密钥",
};
export function summarize(rows: Channel[]) {
  const providers = new Set(rows.map(providerId));
  const eligible = new Set(rows.filter((row) => row.eligible).map(providerId));
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

export const providerId = (row: Pick<Channel, "source_id" | "provider">) =>
  JSON.stringify([row.source_id || "", row.provider]);

export const channelName = (row: Pick<Channel, "provider" | "provider_name">) =>
  row.provider_name || row.provider;
