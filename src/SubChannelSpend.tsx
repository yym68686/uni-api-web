import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { controlRequest, makeLimiter } from "./api";
import type { InstalledChannel } from "./sub2apiImports";
import { providerId } from "./format";
import { Tip } from "./ui";

export interface SubChannelSpend {
  status: "pending" | "complete" | "error" | "unsupported";
  actual_cost_usd: number | null;
  requests: number | null;
  from: number;
  to: number;
  checked_at: number;
  key_id: number;
  scope: "sub2api_business_key";
  scope_label?: string;
  message?: string;
  refreshing?: boolean;
}
export type SubChannelSpendResult = Pick<
  UseQueryResult<SubChannelSpend>,
  "data" | "isPending" | "isError"
>;

export function useSubChannelSpend({
  providers,
  imports,
  session,
  window,
  to,
  refresh,
  auto,
  enabled,
}: {
  providers: string[];
  imports: InstalledChannel[];
  session: string;
  window: string;
  to?: number;
  refresh: number;
  auto: boolean;
  enabled: boolean;
}) {
  const cutoff = useMemo(
    () => to || Math.floor(Date.now() / 1000),
    [to, window, refresh],
  );
  const limit = useMemo(() => makeLimiter(4), []);
  const lookup = useMemo(() => {
    const relevant = new Set(providers);
    const unique = new Map<string, { path: string; label: string }>();
    const mapping = new Map<string, string[]>();
    for (const item of imports) {
      const provider = providerId(item);
      if (!relevant.has(provider)) continue;
      if (item.kind === "configured") {
        if (item.binding_status !== "matched") continue;
        const keys =
          item.binding_status === "matched" ? item.bound_keys || [] : [];
        mapping.set(provider, [
          ...new Set(
            keys.map((bound) => {
              const scope = JSON.stringify([
                bound.account_id,
                "key",
                bound.remote_key_id,
              ]);
              unique.set(scope, {
                path: `/v1/sub2api/accounts/${encodeURIComponent(bound.account_id)}/keys/${bound.remote_key_id}/spend`,
                label: `${bound.account_name} · Key #${bound.remote_key_id}`,
              });
              return scope;
            }),
          ),
        ]);
      } else {
        const scope = JSON.stringify([item.account_id, "group", item.group_id]);
        unique.set(scope, {
          path: `/v1/sub2api/accounts/${encodeURIComponent(item.account_id)}/groups/${item.group_id}/spend`,
          label: "",
        });
        mapping.set(provider, [scope]);
      }
    }
    return { items: [...unique.entries()], mapping };
  }, [providers, imports]);
  const queries = useQueries({
    queries: lookup.items.map(([key, item]) => ({
      queryKey: ["sub-channel-spend", session, key, window, cutoff, refresh],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        limit(
          () =>
            controlRequest<SubChannelSpend>(
              item.path +
                "?" +
                new URLSearchParams({ range: window, to: String(cutoff) }),
              { signal },
            ),
          signal,
        ),
      enabled,
      staleTime: 15000,
      retry: false,
      refetchInterval: (query: {
        state: { data?: SubChannelSpend };
      }): number | false => {
        const result = query.state.data;
        if (result?.status === "pending") return 3000;
        if (result?.status === "error" || result?.refreshing) return 15000;
        return auto ? 30000 : false;
      },
    })),
  });
  const byScope = new Map(
    lookup.items.map(([key], index) => [key, queries[index]]),
  );
  const labels = new Map(lookup.items.map(([key, item]) => [key, item.label]));
  return new Map(
    [...lookup.mapping].map(([provider, keys]) => [
      provider,
      combineChannelSpend(
        keys.map((key) => byScope.get(key)!),
        keys
          .map((key) => labels.get(key)!)
          .filter(Boolean)
          .join("、"),
      ),
    ]),
  );
}

export function combineChannelSpend(
  queries: SubChannelSpendResult[],
  label = "",
): SubChannelSpendResult {
  if (!queries.length)
    return { data: undefined, isPending: false, isError: false };
  if (queries.length === 1) return queries[0];
  const data = queries.map((query) => query.data);
  const first = data.find(Boolean);
  const complete = data.every(
    (item) =>
      item?.status === "complete" &&
      item.actual_cost_usd != null &&
      Number.isFinite(item.actual_cost_usd),
  );
  const error = queries.some(
    (query) => query.isError || query.data?.status === "error",
  );
  return {
    isPending: queries.some((query) => query.isPending),
    isError: error,
    data: first
      ? {
          ...first,
          status: complete ? "complete" : error ? "error" : "pending",
          key_id: 0,
          scope_label: label,
          actual_cost_usd: complete
            ? data.reduce((sum, item) => sum + item!.actual_cost_usd!, 0)
            : null,
          requests: complete
            ? data.reduce((sum, item) => sum + (item!.requests || 0), 0)
            : null,
          checked_at: complete
            ? Math.min(...data.map((item) => item!.checked_at))
            : 0,
          refreshing: data.some((item) => item?.refreshing),
          message: complete
            ? "按已绑定的不同业务 Key 去重汇总。"
            : "正在读取全部已绑定 Key 的账单，未完成前不显示部分金额。",
        }
      : undefined,
  };
}

export function SubChannelSpendValue({
  query,
}: {
  query?: SubChannelSpendResult;
}) {
  const data = query?.data;
  const known =
    data?.status === "complete" &&
    data.actual_cost_usd != null &&
    Number.isFinite(data.actual_cost_usd);
  const date = (value: number) =>
    new Date(value * 1000).toLocaleString("zh-CN", { hour12: false });
  const explanation = data
    ? `${data.scope_label || `sub2api 业务 Key #${data.key_id}`} · ${date(data.from)} 至 ${date(data.to)}。${known ? `${data.requests ?? 0} 条账单；` : ""}按该业务 Key 整体统计，包含所有模型和调用来源，共用 Key 的渠道不可重复相加。${data.checked_at ? `账单更新于 ${date(data.checked_at)}。` : ""}${query?.isError ? "更新失败，显示上次完整统计。" : data.message || ""}`
    : query?.isError
      ? "站点账单查询失败，请刷新重试。"
      : query
        ? "正在读取所选时间范围的站点账单。"
        : "尚未确认此渠道的 sub2api 账号关联。";
  let label = "未确认";
  if (known)
    label = `$${data.actual_cost_usd!.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 10 })}`;
  else if (query?.isPending || data?.status === "pending") label = "同步账单";
  else if (query?.isError || data?.status === "error") label = "查询失败";
  return (
    <Tip text={explanation}>
      <span className={known ? "mono" : "muted"}>{label}</span>
    </Tip>
  );
}
