import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import {
  decodeManagementPage,
  managementPageMediaType,
} from "./managementQueryWire";
import type {
  ManagementRow,
  ManagedChannel,
  ConfiguredCheck,
} from "./channelManagement";
import type { SubAccount } from "./Sub2apiChecks";
import type { SubFilters } from "./sub2apiPreferences";
import type { InstalledChannel } from "./sub2apiImports";
import type { Routes } from "./channelRouteData";
import type { ModelPrice } from "./types";
import type { OptimizationSnapshot } from "./channelOptimizationPlan";
import type { FingerprintTarget, FingerprintRecord } from "./ModelFingerprints";

export interface ManagementPage {
  format: "management-page-v1";
  owner?: string;
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
  context: {
    prices: ModelPrice[];
    imports: InstalledChannel[];
    routes: Record<string, Routes>;
    fingerprints: Record<
      string,
      { target: FingerprintTarget; record?: FingerprintRecord }[]
    >;
    fingerprint_models: string[];
  };
}

declare global {
  var __uniManagementBootstrap:
    | {
        owner: string;
        filters: string;
        startedAt: number;
        controller: AbortController;
        result: Promise<ManagementPage>;
      }
    | undefined;
}

const managementReadTimings = new WeakMap<
  ManagementPage,
  { started: number; decoded: number }
>();

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

const path = (
  filters: SubFilters,
  token?: { cursor?: string; selection?: string },
) => {
  const query = new URLSearchParams({ filters: JSON.stringify(filters) });
  if (token?.cursor) query.set("cursor", token.cursor);
  if (token?.selection) query.set("selection", token.selection);
  return `/v1/channel-management/query?${query}`;
};
const count = (value: unknown): value is number =>
  Number.isSafeInteger(value) && (value as number) >= 0;
const uniqueRows = (rows: ManagementRow[]) =>
  rows.every(
    (row) =>
      typeof row?.id === "string" &&
      row.id &&
      row.account &&
      row.target &&
      Array.isArray(row.checks),
  ) && new Set(rows.map((row) => row.id)).size === rows.length;

export async function readManagementPage(
  filters: SubFilters,
  cursor: string,
  signal: AbortSignal,
  owner?: string,
): Promise<ManagementPage> {
  if (owner) {
    const bootstrap = globalThis.__uniManagementBootstrap;
    delete globalThis.__uniManagementBootstrap;
    if (bootstrap) {
      if (
        !cursor &&
        bootstrap.owner === owner &&
        bootstrap.filters === JSON.stringify(filters) &&
        Date.now() - bootstrap.startedAt < 10_000
      ) {
        const abort = () => bootstrap.controller.abort(signal.reason);
        if (signal.aborted) abort();
        else signal.addEventListener("abort", abort, { once: true });
        try {
          const page = validateManagementPage(await bootstrap.result);
          signal.throwIfAborted();
          // Older backends do not attest the owner, so negotiate a normal
          // authenticated read rather than accept an unbound early response.
          if (page.owner === owner) return page;
        } finally {
          signal.removeEventListener("abort", abort);
        }
      }
      bootstrap.controller.abort();
    }
  }
  const started = performance.now();
  const response = await controlRequest<unknown>(path(filters, { cursor }), {
    signal,
    cache: "no-cache",
    headers: {
      Accept: managementPageMediaType,
      "X-Console-Current-Validation": "1",
    },
  });
  signal.throwIfAborted();
  const page = validateManagementPage(response);
  if (owner && page.owner && page.owner !== owner)
    throw Error("登录账号已变化，请重新读取");
  managementReadTimings.set(page, { started, decoded: performance.now() });
  return page;
}

function validateManagementPage(response: unknown): ManagementPage {
  const page = decodeManagementPage(response);
  if (
    page.format !== "management-page-v1" ||
    !page.revision ||
    !page.selection_token ||
    !count(page.read_at_ms) ||
    !count(page.total) ||
    !count(page.eligible_count) ||
    page.eligible_count > page.total ||
    !count(page.offset) ||
    page.offset % 25 ||
    page.page_size !== 25 ||
    page.offset > page.total ||
    !Array.isArray(page.rows) ||
    page.rows.length !== Math.min(25, page.total - page.offset) ||
    !uniqueRows(page.rows) ||
    !Array.isArray(page.accounts) ||
    page.accounts.some(
      (account) =>
        !Array.isArray(account.targets) || account.targets.length > 0,
    ) ||
    !Array.isArray(page.rates) ||
    !Array.isArray(page.platforms) ||
    !Array.isArray(page.models) ||
    !Array.isArray(page.detection_models) ||
    (page.offset + page.rows.length < page.total && !page.next_cursor) ||
    (page.offset > 0 && !page.previous_cursor)
  ) {
    throw Error("渠道分页数据不完整，请重新读取");
  }
  if (
    !page.context ||
    !Array.isArray(page.context.prices) ||
    !Array.isArray(page.context.imports) ||
    !page.context.routes ||
    !page.context.fingerprints ||
    !Array.isArray(page.context.fingerprint_models) ||
    page.rows.some((row) => !Array.isArray(page.context.fingerprints[row.id]))
  )
    throw Error("渠道显示数据不完整，请重新读取");
  return page;
}

// A page is visible only after this complete read succeeds. Cached rows are
// never presented as current after a remount, filter change or refresh error.
export function useManagementPage(
  owner: string,
  filters: SubFilters,
  cursor = "",
) {
  const client = useQueryClient();
  useEffect(() => {
    const refresh = () => {
      void client.invalidateQueries({ queryKey: ["management-query", owner] });
    };
    window.addEventListener("uni-console-control-write", refresh);
    return () =>
      window.removeEventListener("uni-console-control-write", refresh);
  }, [client, owner]);
  const query = useQuery({
    queryKey: ["management-query", owner, filters, cursor],
    queryFn: ({ signal }) => readManagementPage(filters, cursor, signal, owner),
    enabled: !!owner,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnMount: "always",
    refetchOnWindowFocus: true,
    structuralSharing: false,
    refetchInterval: (q) =>
      q.state.data?.accounts.some(
        (a) => a.state === "queued" || a.state === "running",
      )
        ? 5000
        : 60000,
  });
  useEffect(() => {
    const page = query.data;
    if (!owner || query.isFetching || query.isError || !page) return;
    const timing = managementReadTimings.get(page);
    if (!timing) return;
    let second = 0;
    const first = requestAnimationFrame(() => {
      second = requestAnimationFrame(() => {
        const painted = performance.now();
        // Local diagnostics only: no owner, URL, filters, credentials or
        // response contents are logged or transmitted anywhere.
        console.info(
          "management_page_current",
          JSON.stringify({
            read_at_ms: page.read_at_ms,
            rows: page.rows.length,
            total: page.total,
            request_to_paint_ms: Math.round(painted - timing.started),
            decoded_to_paint_ms: Math.round(painted - timing.decoded),
            navigation_to_paint_ms: Math.round(painted),
          }),
        );
      });
    });
    return () => {
      cancelAnimationFrame(first);
      cancelAnimationFrame(second);
    };
  }, [owner, query.data, query.isFetching, query.isError]);
  return {
    ...query,
    data: !owner || query.isFetching || query.isError ? undefined : query.data,
  };
}

export async function readManagementSelection(
  filters: SubFilters,
  page: ManagementPage,
  signal: AbortSignal,
): Promise<ManagementSelection> {
  const selection = await controlRequest<ManagementSelection>(
    path(filters, { selection: page.selection_token }),
    { signal },
  );
  signal.throwIfAborted();
  if (
    selection.format !== "management-selection-v1" ||
    selection.revision !== page.revision ||
    !count(selection.read_at_ms) ||
    selection.total !== page.total ||
    selection.eligible_count !== page.eligible_count ||
    !Array.isArray(selection.rows) ||
    selection.rows.length !== selection.total ||
    !uniqueRows(selection.rows) ||
    !Array.isArray(selection.eligible) ||
    selection.eligible.length !== selection.eligible_count ||
    !uniqueRows(selection.eligible) ||
    !selection.input ||
    !Array.isArray(selection.input.accounts) ||
    !Array.isArray(selection.input.channels) ||
    !Array.isArray(selection.input.imports) ||
    !Array.isArray(selection.input.checks) ||
    !selection.input.routes ||
    !Array.isArray(selection.prices)
  ) {
    throw Error("完整筛选结果已变化或读取不完整，请刷新渠道后重试");
  }
  const all = new Set(selection.rows.map((row) => row.id));
  if (selection.eligible.some((row) => !all.has(row.id)))
    throw Error("检测选择与筛选结果不一致，请重新读取");
  return selection;
}

export function managementOptimizationSnapshot(
  selection: ManagementSelection,
  filters: SubFilters,
): OptimizationSnapshot {
  return {
    rows: selection.rows,
    accounts: selection.input.accounts,
    inventory: selection.input.channels,
    imports: {
      data: selection.input.imports,
      labels: {},
      unavailable_sources: selection.input.unavailable_imports || [],
    },
    routes: selection.input.routes,
    checks: selection.input.checks,
    prices: selection.prices,
    filters: { ...filters },
  };
}
