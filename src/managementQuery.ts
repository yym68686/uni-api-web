import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { ManagementRow, ManagedChannel, ConfiguredCheck } from "./channelManagement";
import type { SubAccount } from "./Sub2apiChecks";
import type { SubFilters } from "./sub2apiPreferences";
import type { InstalledChannel } from "./sub2apiImports";
import type { Routes } from "./channelRouteData";
import type { ModelPrice } from "./types";
import type { OptimizationSnapshot } from "./channelOptimizationPlan";

export interface ManagementPage {
  format: "management-page-v1";
  revision: string;
  read_at_ms: number;
  rows: ManagementRow[];
  total: number;
  eligible_count: number;
  offset: number;
  page_size: 25;
  next_cursor?: string;
  previous_cursor?: string;
  selection_token: string;
  accounts: SubAccount[];
  rates: number[];
  platforms: string[];
  models: string[];
  detection_models: string[];
}

export interface ManagementSelection {
  format: "management-selection-v1";
  revision: string;
  read_at_ms: number;
  rows: ManagementRow[];
  eligible: ManagementRow[];
  total: number;
  eligible_count: number;
  input: {
    accounts: SubAccount[];
    channels: ManagedChannel[];
    imports: InstalledChannel[];
    checks: ConfiguredCheck[];
    routes: Record<string, Routes>;
    unavailable_sources: string[] | null;
    unavailable_imports: string[] | null;
  };
  prices: ModelPrice[];
}

const path = (filters: SubFilters, token?: { cursor?: string; selection?: string }) => {
  const query = new URLSearchParams({ filters: JSON.stringify(filters) });
  if (token?.cursor) query.set("cursor", token.cursor);
  if (token?.selection) query.set("selection", token.selection);
  return `/v1/channel-management/query?${query}`;
};
const count = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0;
const uniqueRows = (rows: ManagementRow[]) => rows.every(row => typeof row?.id === "string" && row.id && row.account && row.target && Array.isArray(row.checks)) && new Set(rows.map(row => row.id)).size === rows.length;

export async function readManagementPage(filters: SubFilters, cursor: string, signal: AbortSignal): Promise<ManagementPage> {
  const page = await controlRequest<ManagementPage>(path(filters, { cursor }), { signal });
  signal.throwIfAborted();
  if (page.format !== "management-page-v1" || !page.revision || !page.selection_token || !count(page.read_at_ms) || !count(page.total) || !count(page.eligible_count) || page.eligible_count > page.total || !count(page.offset) || page.offset % 25 || page.page_size !== 25 || page.offset > page.total || !Array.isArray(page.rows) || page.rows.length !== Math.min(25, page.total - page.offset) || !uniqueRows(page.rows) || !Array.isArray(page.accounts) || page.accounts.some(account => !Array.isArray(account.targets) || account.targets.length > 0) || !Array.isArray(page.rates) || !Array.isArray(page.platforms) || !Array.isArray(page.models) || !Array.isArray(page.detection_models) || (page.offset + page.rows.length < page.total && !page.next_cursor) || (page.offset > 0 && !page.previous_cursor)) {
    throw Error("渠道分页数据不完整，请重新读取");
  }
  return page;
}

// A page is visible only after this complete read succeeds. Cached rows are
// never presented as current after a remount, filter change or refresh error.
export function useManagementPage(owner: string, filters: SubFilters, cursor = "") {
  const query = useQuery({
    queryKey: ["management-query", owner, filters, cursor],
    queryFn: ({ signal }) => readManagementPage(filters, cursor, signal),
    enabled: !!owner,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnMount: "always",
    refetchOnWindowFocus: true,
    structuralSharing: false,
  });
  return { ...query, data: !owner || query.isFetching || query.isError ? undefined : query.data };
}

export async function readManagementSelection(filters: SubFilters, page: ManagementPage, signal: AbortSignal): Promise<ManagementSelection> {
  const selection = await controlRequest<ManagementSelection>(path(filters, { selection: page.selection_token }), { signal });
  signal.throwIfAborted();
  if (selection.format !== "management-selection-v1" || selection.revision !== page.revision || !count(selection.read_at_ms) || selection.total !== page.total || selection.eligible_count !== page.eligible_count || !Array.isArray(selection.rows) || selection.rows.length !== selection.total || !uniqueRows(selection.rows) || !Array.isArray(selection.eligible) || selection.eligible.length !== selection.eligible_count || !uniqueRows(selection.eligible) || !selection.input || !Array.isArray(selection.input.accounts) || !Array.isArray(selection.input.channels) || !Array.isArray(selection.input.imports) || !Array.isArray(selection.input.checks) || !selection.input.routes || !Array.isArray(selection.prices)) {
    throw Error("完整筛选结果已变化或读取不完整，请刷新渠道后重试");
  }
  const all = new Set(selection.rows.map(row => row.id));
  if (selection.eligible.some(row => !all.has(row.id))) throw Error("检测选择与筛选结果不一致，请重新读取");
  return selection;
}

export function managementOptimizationSnapshot(selection: ManagementSelection, filters: SubFilters): OptimizationSnapshot {
  return {
    rows: selection.rows,
    accounts: selection.input.accounts,
    inventory: selection.input.channels,
    imports: { data: selection.input.imports, labels: {}, unavailable_sources: selection.input.unavailable_imports || [] },
    routes: selection.input.routes,
    checks: selection.input.checks,
    prices: selection.prices,
    filters: { ...filters },
  };
}
