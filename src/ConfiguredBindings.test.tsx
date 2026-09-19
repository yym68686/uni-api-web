import { expect, it, vi } from "vitest";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import type { ReactNode } from "react";
import { ChannelAccess } from "./ChannelAccess";
import { combineChannelSpend, useSubChannelSpend } from "./SubChannelSpend";
import type { SubChannelSpendResult } from "./SubChannelSpend";
import { useChannelAccountBalances } from "./ChannelAccountBalances";
import { withSubQuality } from "./ChannelChecks";
import type { Checks } from "./ChannelChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import type { Channel } from "./types";
import { providerId, balanceIsLow } from "./format";

const row = {
  source_id: "source",
  provider: "configured",
  model: "gpt-6-astra",
} as Channel;
const bound: InstalledChannel = {
  source_id: "source",
  source_name: "Source",
  provider: "configured",
  kind: "configured",
  binding_status: "matched",
  account_id: "account",
  group_id: 7,
  models: [],
  manageable: false,
  api_key_id: "",
  key_position: 0,
  key_prefix: "",
  name: "configured",
  positions: {},
  revision: "",
  bound_keys: [42, 43].map((remote_key_id) => ({
    account_id: "account",
    account_name: "站点账号",
    base: "https://site.test",
    group_id: 7,
    remote_key_id,
  })),
};
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <Tooltip.Provider>{children}</Tooltip.Provider>
    </QueryClientProvider>
  );
}
function spend(amount: number): SubChannelSpendResult {
  return {
    data: {
      status: "complete",
      actual_cost_usd: amount,
      requests: 1,
      checked_at: 200,
      from: 100,
      to: 200,
      key_id: 42,
      scope: "sub2api_business_key",
    },
    isPending: false,
    isError: false,
  };
}
it("only publishes a configured channel total when every bound key has a complete receipt window", () => {
  expect(combineChannelSpend([spend(1), spend(2)]).data?.actual_cost_usd).toBe(
    3,
  );
  const pending = {
    ...spend(2),
    data: {
      ...spend(2).data!,
      status: "pending" as const,
      actual_cost_usd: null,
    },
  };
  expect(combineChannelSpend([spend(1), pending]).data).toMatchObject({
    status: "pending",
    actual_cost_usd: null,
    requests: null,
  });
  expect(
    combineChannelSpend([
      spend(1),
      { data: undefined, isPending: false, isError: true },
    ]).data?.actual_cost_usd,
  ).toBeNull();
});
it("uses exact existing key endpoints and deduplicates credentials shared across providers", async () => {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      return new Response(
        JSON.stringify(spend(input.includes("/42/") ? 1 : 2).data),
      );
    }),
  );
  const second = {
    ...bound,
    provider: "same-key",
    bound_keys: [bound.bound_keys![0]],
  };
  const partial = {
    ...bound,
    provider: "partial",
    binding_status: "partial" as const,
  };
  const imports = [bound, second, partial];
  const { result } = renderHook(
    () =>
      useSubChannelSpend({
        providers: imports.map(providerId),
        imports,
        session: "fixture",
        window: "2h",
        to: 200,
        refresh: 0,
        auto: false,
        enabled: true,
      }),
    { wrapper },
  );
  await waitFor(() =>
    expect(result.current.get(providerId(bound))?.data?.actual_cost_usd).toBe(
      3,
    ),
  );
  expect(result.current.get(providerId(second))?.data?.actual_cost_usd).toBe(1);
  expect(result.current.has(providerId(partial))).toBe(false);
  expect(urls).toHaveLength(2);
  expect(
    urls.every((url) => url.includes("/keys/") && url.includes("range=2h")),
  ).toBe(true);
});
it("uses account balances once across shared keys and retains negative balance filtering", async () => {
  const fetch = vi.fn(
    async () =>
      new Response(
        JSON.stringify({ status: "ok", amount: -1, checked_at: 200 }),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  const channels = [bound, { ...bound, provider: "shared" }];
  const { result } = renderHook(
    () =>
      useChannelAccountBalances(
        channels.map(providerId),
        channels,
        "fixture",
        true,
        false,
      ),
    { wrapper },
  );
  await waitFor(() =>
    expect(result.current.get(providerId(bound))?.data?.keys?.[0].amount).toBe(
      -1,
    ),
  );
  const balance = result.current.get(providerId(bound))!.data!;
  expect(balance.keys).toHaveLength(1);
  expect(balanceIsLow(balance)).toBe(true);
  expect(fetch).toHaveBeenCalledTimes(1);
});
it("shows persistent account associations without offering temporary-channel removal", () => {
  render(
    <ChannelAccess
      row={row}
      imports={{ data: { data: [bound] } } as SubImportsQuery}
      onRemoved={vi.fn()}
    />,
    { wrapper },
  );
  expect(screen.getByText("已自动关联，使用账号查询账单与余额")).toBeVisible();
  expect(screen.getByText("站点账号 · Key #42 · 分组 #7")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "从此 API key 移除" }),
  ).not.toBeInTheDocument();
});
it("combines each bound group's quality history once even when several keys share it", () => {
  const checks = { results: new Map() } as unknown as Checks;
  const summary = {
    account_id: "account",
    group_id: 7,
    history: { total: 4, successful: 3, passed: 2 },
    check: {
      ...row,
      source_id: "",
      verdict: "pass" as const,
      checked_at: 300,
      text: "21",
      duration_ms: 1,
    },
  };
  expect(
    withSubQuality(checks, [bound], [summary]).results.get(providerId(row))
      ?.history,
  ).toEqual(summary.history);
  expect(
    withSubQuality(
      checks,
      [{ ...bound, binding_status: "partial" }],
      [summary],
    ).results.has(providerId(row)),
  ).toBe(false);
});
