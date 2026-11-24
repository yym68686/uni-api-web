export const serviceTiers = ["fast", "ultrafast"] as const;
export type ServiceTier = typeof serviceTiers[number];
export const serviceTierName = { fast: "Fast", ultrafast: "Ultrafast" } as const;
export const serviceTierFilterLabels = {
  supported: "已验证支持",
  not_honored: "未命中",
  unsupported: "明确不支持",
  unknown: "未确认",
} as const;
export interface ServiceTierResult {
  status: keyof typeof serviceTierFilterLabels | "inconclusive" | "not_applicable";
  observed?: string;
  checked_at: number;
  binding?: string;
  verified_binding?: string;
  verified_at?: number;
}
export const serviceTierFresh = (at?: number, now = Math.floor(Date.now() / 1000)) =>
  !!at && at > 0 && at <= now && now - at < 7 * 24 * 60 * 60;
export const serviceTierVerified = (result?: ServiceTierResult | null) =>
  !!result?.binding && result.binding === result.verified_binding &&
  result.status !== "unsupported" && serviceTierFresh(result.verified_at);
