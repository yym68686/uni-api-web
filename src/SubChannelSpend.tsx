import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { controlRequest, makeLimiter } from "./api";
import type { Channel, Metrics } from "./types";
import type { InstalledChannel } from "./sub2apiImports";
import { providerId, rowId } from "./format";
import { Tip } from "./ui";

export interface SubChannelSpend {
  status:
    | "pending"
    | "complete"
    | "error"
    | "unsupported"
    | "unmatched"
    | "ambiguous"
    | "no_records";
  actual_cost_usd: number | null;
  requests?: number | null;
  from: number;
  to: number;
  checked_at?: number;
  key_id?: number;
  scope: "sub2api_business_key" | "matched_requests";
  pending_attempts?: number;
  absent_receipt_attempts?: number;
  absent_receipt_statuses?: Record<string, number>;
  missing_correlation_attempts?: number;
  missing_response_identifiers?: number;
  missing_response_statuses?: Record<string, number>;
  unbound_attempts?: number;
  sync_error?: boolean;
  total_attempts?: number;
  matched_attempts?: number;
  confirmed_unbilled_attempts?: number;
  missing_identifiers?: number;
  ambiguous_attempts?: number;
  matched_cost_usd?: number;
  scope_label?: string;
  message?: string;
  refreshing?: boolean;
  unresolved_samples?: {
    request_id: string;
    attempt_id: string;
    endpoint: string;
    status: number;
    at: number;
    reason: string;
  }[];
}
export type SubChannelSpendResult = Pick<
  UseQueryResult<SubChannelSpend>,
  "data" | "isPending" | "isError"
>;

// One scoped request for the table. Business-key totals remain a separate API
// for account views; they must never feed per-caller channel profit.
export function useScopedChannelSpend({
  rows,
  session,
  sourceId,
  keyId,
  model,
  endpoint,
  stream,
  from,
  to,
  refresh,
  auto,
  enabled,
  snapshot,
  snapshotError = false,
  snapshotUpdatedAt,
}: {
  snapshot?: Metrics;
  snapshotError?: boolean;
  snapshotUpdatedAt?: number;
  rows: Channel[];
  session: string;
  sourceId: string;
  keyId: string;
  model: string;
  endpoint: string;
  stream: string;
  from?: number;
  to?: number;
  refresh: number;
  auto: boolean;
  enabled: boolean;
}) {
  const keyParts = keyId.split("::");
  const params = new URLSearchParams({
    source_id: keyParts.length === 2 ? keyParts[0] : sourceId,
    key_id: keyParts.at(-1) || "",
    model,
    endpoint,
    stream,
    from: String(from || 0),
    to: String(to || 0),
  });
  const combined =
    snapshot?.channel_spend !== undefined || !!snapshot?.channel_spend_error;
  const query = useQuery({
    queryKey: ["channel-spend", session, params.toString(), refresh],
    queryFn: ({ signal }) =>
      controlRequest<{
        data: (SubChannelSpend & {
          source_id: string;
          provider: string;
          model: string;
          upstream_model: string;
        })[];
      }>("/v1/channel-spend?" + params, { signal }),
    initialData: snapshot?.channel_spend
      ? {
          data: snapshot.channel_spend.map((item) => ({
            ...item,
            source_id: item.source_id || "",
          })),
        }
      : undefined,
    initialDataUpdatedAt: snapshotUpdatedAt,
    enabled:
      enabled && !snapshot?.channel_spend_error && from != null && to != null,
    staleTime: 15000,
    retry: false,
    refetchInterval: (query) => {
      const data = query.state.data?.data || [];
      if (data.some((r) => r.sync_error || r.status === "error")) return 15000;
      // Missing old observations must not mask receipts that are still syncing.
      // The fallback also works during a rolling update of the backend.
      if (
        data.some(
          (r) =>
            r.refreshing ||
            r.status === "pending" ||
            (r.status !== "complete" &&
              (r.pending_attempts ??
                Math.max(
                  0,
                  (r.total_attempts || 0) -
                    (r.matched_attempts || 0) -
                    (r.confirmed_unbilled_attempts || 0) -
                    (r.missing_identifiers || 0) -
                    (r.ambiguous_attempts || 0) -
                    (r.absent_receipt_attempts || 0),
                )) > 0),
        )
      )
        return 5000;
      if (data.some((r) => (r.absent_receipt_attempts || 0) > 0)) return 60000;
      return auto ? 30000 : false;
    },
  });
  const identity = (
    item: Pick<Channel, "source_id" | "provider" | "model" | "upstream_model">,
  ) =>
    JSON.stringify([
      item.source_id || "",
      item.provider,
      item.model,
      item.upstream_model,
    ]);
  const found = new Map(
    (snapshot?.channel_spend_error
      ? []
      : snapshot?.channel_spend &&
          (snapshotUpdatedAt ?? Infinity) >= query.dataUpdatedAt
        ? snapshot.channel_spend
        : query.data?.data || []
    ).map((item) => [identity(item), item]),
  );
  return new Map(
    rows.map((row) => [
      rowId(row),
      {
        data:
          found.get(identity(row)) ||
          ((combined ? snapshot?.channel_spend : query.data)
            ? {
                status: "no_records" as const,
                actual_cost_usd: null,
                requests: null,
                from: from || 0,
                to: to || 0,
                checked_at: 0,
                key_id: 0,
                scope: "matched_requests" as const,
                message: "当前范围暂无账单关联记录，不能确认消费金额",
              }
            : undefined),
        isPending: combined ? false : query.isPending,
        isError: combined
          ? !!snapshot?.channel_spend_error || snapshotError
          : query.isError,
      },
    ]),
  );
}

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
            ? Math.min(...data.map((item) => item!.checked_at || 0))
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
  const verified =
    (data?.matched_attempts || 0) + (data?.confirmed_unbilled_attempts || 0);
  const partial =
    !known &&
    data?.scope === "matched_requests" &&
    verified > 0 &&
    typeof data.matched_cost_usd === "number" &&
    Number.isFinite(data.matched_cost_usd) &&
    data.matched_cost_usd >= 0;
  const date = (value: number) =>
    new Date(value * 1000).toLocaleString("zh-CN", { hour12: false });
  const money = (value: number) =>
    `$${value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 10 })}`;
  const missing =
    data?.scope === "matched_requests"
      ? [
          data.missing_correlation_attempts
            ? `缺少关联事实 ${data.missing_correlation_attempts} 次（旧版历史无法补回）`
            : "",
          data.missing_response_identifiers
            ? `上游未返回账单标识 ${data.missing_response_identifiers} 次${
                data.missing_response_statuses
                  ? `（${Object.entries(data.missing_response_statuses)
                      .map(
                        ([status, n]) =>
                          `${status === "0" ? "未取得响应头" : `HTTP ${status}`}：${n}`,
                      )
                      .join("、")}）`
                  : ""
              }`
            : "",
          data.unbound_attempts
            ? `账号关联未确认 ${data.unbound_attempts} 次`
            : "",
          data.ambiguous_attempts
            ? `关联冲突 ${data.ambiguous_attempts} 次`
            : "",
          data.pending_attempts
            ? `账单同步尚未覆盖 ${data.pending_attempts} 次`
            : "",
          data.absent_receipt_attempts
            ? `已完成相应时间段的账单同步，但未找到 ${data.absent_receipt_attempts} 次请求的账单${
                data.absent_receipt_statuses
                  ? `（${Object.entries(data.absent_receipt_statuses)
                      .map(([status, n]) => `HTTP ${status}：${n}`)
                      .join("、")}）`
                  : ""
              }；会低频复查，不能据此认定免费`
            : "",
          data.unresolved_samples?.length
            ? `未核对示例：${data.unresolved_samples
                .slice(0, 3)
                .map(
                  (item) =>
                    `${date(item.at)} ${item.endpoint} HTTP ${item.status || "无响应"} · ${item.attempt_id}`,
                )
                .join("；")}`
            : "",
        ]
          .filter(Boolean)
          .join("；")
      : "";
  const explanation =
    data?.scope === "matched_requests"
      ? `${date(data.from)} 至 ${date(data.to)}。按当前来源、调用 Key、渠道、模型、端点及流式范围逐请求关联。已核对 ${verified}/${data.total_attempts ?? 0} 次上游尝试。${data.confirmed_unbilled_attempts ? `其中 ${data.confirmed_unbilled_attempts} 次经错误响应确认在模型调用前因余额不足被拒绝，消费为零。` : ""}${missing ? missing + "。" : data.message || ""}${data.checked_at ? `上次完整账单同步：${date(data.checked_at)}。` : ""}${(data.missing_response_identifiers || 0) > 0 ? "响应缺少关联标识，继续等待同步不能补回这些标识。" : ""}${data.sync_error ? "站点账单查询失败，后台会重试。" : ""}${partial ? `至少 ${money(data.matched_cost_usd!)}；这是已核对部分，不是完整消费。利润列据此显示上限，实际利润可能更低。` : ""}${query?.isError ? "本次刷新失败，当前为上次核对结果。" : ""}`
      : data
        ? `${data.scope_label || `sub2api 业务 Key #${data.key_id}`} · ${date(data.from)} 至 ${date(data.to)}。${known ? `${data.requests ?? 0} 条账单；` : ""}按该业务 Key 整体统计，包含所有模型和调用来源，共用 Key 的渠道不可重复相加。${data.checked_at ? `账单更新于 ${date(data.checked_at)}。` : ""}${query?.isError ? "更新失败，显示上次完整统计。" : data.message || ""}`
        : query?.isError
          ? "站点账单查询失败，请刷新重试。"
          : query
            ? "正在读取所选时间范围的站点账单。"
            : "尚未确认此渠道的 sub2api 账号关联。";
  let label = "未确认";
  if (known) label = money(data.actual_cost_usd!);
  else if (partial) label = `≥${money(data.matched_cost_usd!)}`;
  else if (query?.isError || data?.status === "error" || data?.sync_error)
    label = "查询失败";
  else if (query?.isPending || data?.status === "pending" || data?.refreshing)
    label = "同步账单";
  else if (data?.status === "ambiguous") label = "关联冲突";
  else if (data?.status === "no_records") label = "—";
  else if (data?.missing_correlation_attempts) label = "历史未关联";
  else if (data?.unbound_attempts) label = "未关联账号";
  else if (data?.missing_response_identifiers) label = "缺少响应标识";
  else if (data?.absent_receipt_attempts) label = "未找到账单";
  else if (data?.status === "unmatched") label = "无法归属";
  return (
    <Tip text={explanation}>
      <span className={`channel-spend ${known || partial ? "mono" : "muted"}`}>
        <span>{label}</span>
        {partial && (
          <small className="muted">
            部分 · {verified}/{data.total_attempts}
          </small>
        )}
      </span>
    </Tip>
  );
}
