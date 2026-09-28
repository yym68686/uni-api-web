import { expect, it, vi } from "vitest";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import type { ReactNode } from "react";
import { ChannelAccess } from "./ChannelAccess";
import { combineChannelSpend, useSubChannelSpend } from "./SubChannelSpend";
import type { SubChannelSpendResult } from "./SubChannelSpend";
import { useChannelAccountBalances, withAccountWallet } from "./ChannelAccountBalances";
import { accountBalanceQuery } from "./accountBalance";
import { withSubQuality } from "./ChannelChecks";
import type { Checks } from "./ChannelChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import type { Balance, Channel } from "./types";
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
it("shares one wallet and timestamp across configured, copied and imported channels and refreshes them together", async () => {
  let wallet = { status: "ok", amount: -0.0584, checked_at: 100 };
  const fetch = vi.fn(async () => new Response(JSON.stringify(wallet)));
  vi.stubGlobal("fetch", fetch);
  const imported: InstalledChannel = {
    ...bound, source_id: "digitalocean", provider: "sub2api-imported",
    kind: undefined, binding_status: undefined, bound_keys: undefined,
    base: "https://site.test",
  };
  const channels = [bound, { ...bound, provider: "sub2api-copy" }, imported];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { result, rerender } = renderHook(
    ({ providers }) => useChannelAccountBalances(providers, channels, "wallet-user", true, false),
    { initialProps: { providers: channels.map(providerId) },
      wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> },
  );
  await waitFor(() => expect(result.current.get(providerId(imported))?.data?.keys?.[0]).toMatchObject({
    amount: -0.0584, checked_at: 100, kind: "account_wallet", label: "站点账号",
  }));
  expect(fetch).toHaveBeenCalledTimes(1);
  wallet = { status: "ok", amount: 99.9416, checked_at: 200 };
  await result.current.get(providerId(imported))!.refetch();
  await waitFor(() => {
    for (const channel of channels) {
      const balance = result.current.get(providerId(channel))!.data!;
      expect(balance.keys).toHaveLength(1);
      expect(balance.keys![0]).toMatchObject({ amount: 99.9416, checked_at: 200 });
      expect(balanceIsLow(balance)).toBe(false);
    }
  });
  // Changing the source/model view does not create a separate account wallet.
  rerender({ providers: [providerId(imported)] });
  expect(result.current.get(providerId(imported))?.data?.keys?.[0].amount).toBe(99.9416);
  // The account list also consumes the exact same query/cache entry.
  expect(await client.fetchQuery(accountBalanceQuery("wallet-user", "account", "https://site.test"))).toEqual(wallet);
  expect(fetch).toHaveBeenCalledTimes(2);
});
it("keeps accounts on the same site separate and does not turn partial bindings into account wallets", async () => {
  const fetch = vi.fn(async (input: string) => new Response(JSON.stringify({
    status: "ok", amount: input.includes("/other/") ? 10 : 20, checked_at: 200,
  })));
  vi.stubGlobal("fetch", fetch);
  const other: InstalledChannel = { ...bound, provider: "other", kind: undefined, binding_status: undefined, bound_keys: undefined, account_id: "other", base: "https://site.test" };
  const partial = { ...bound, provider: "partial", binding_status: "partial" as const };
  const ambiguous = { ...bound, provider: "ambiguous", binding_status: "ambiguous" as const };
  const channels = [bound, other, partial, ambiguous];
  const { result } = renderHook(() => useChannelAccountBalances(channels.map(providerId), channels, "separate", true, false), { wrapper });
  await waitFor(() => expect(result.current.get(providerId(other))?.data?.keys?.[0].amount).toBe(10));
  expect(result.current.get(providerId(bound))?.data?.keys?.[0].amount).toBe(20);
  expect(result.current.has(providerId(partial))).toBe(false);
  expect(result.current.has(providerId(ambiguous))).toBe(false);
  expect(fetch).toHaveBeenCalledTimes(2);
});
it("replaces only gateway wallets and preserves key quotas, subscriptions and receipt totals", async () => {
  const quota = { position: 2, kind: "key_quota", status: "ok", amount: 2, checked_at: 90 };
  const subscription = { position: 3, kind: "subscription", status: "ok", amount: 3, checked_at: 90 };
  const raw = {
    data: { provider: "imported", status: "complete", actual_cost_usd: 12, keys: [
      { position: 1, kind: "wallet", status: "ok", amount: -0.0584, checked_at: 90, actual_cost_usd: 4 },
      quota, subscription,
    ] } as Balance,
    isPending: false, isError: false, refetch: vi.fn(async () => {}),
  };
  const account = {
    needsChannelBalance: true,
    data: { provider: "imported", status: "complete", keys: [
      { position: 1, kind: "account_wallet", status: "ok", amount: 99.9416, checked_at: 200 },
    ] } as Balance,
    isPending: false, isError: false, refetch: vi.fn(async () => {}),
  };
  const result = withAccountWallet(raw, account);
  expect(result.data?.keys?.[0]).toMatchObject({ amount: 99.9416, checked_at: 200, actual_cost_usd: 4 });
  expect(result.data?.keys?.slice(1)).toEqual([quota, subscription]);
  expect(result.data?.actual_cost_usd).toBe(12);
  expect(raw.data.keys?.[0].amount).toBe(-0.0584);
  await result.refetch();
  expect(account.refetch).toHaveBeenCalledTimes(1);
  expect(raw.refetch).toHaveBeenCalledTimes(1);
  for (const kind of ["key_quota", "subscription", "rate_limits", undefined]) {
    const other = { ...raw, data: { ...raw.data, keys: [{ ...quota, kind }] } };
    expect(withAccountWallet(other, account)).toBe(other);
  }
  expect(withAccountWallet(raw)).toBe(raw);
  expect(withAccountWallet(raw, { ...account, data: undefined, isPending: true })).toMatchObject({ data: undefined, isPending: true });
  expect(withAccountWallet(raw, { ...account, isError: true })).toMatchObject({ data: undefined, isError: true });
});
it("scans different account sites concurrently and keeps accounts on the same site serial", async () => {
  const finish = new Map<string, () => void>();
  const fetch = vi.fn((input: string) => new Promise<Response>((resolve) => {
    const id = input.split("/accounts/")[1].split("/")[0];
    finish.set(id, () => resolve(new Response(JSON.stringify({ status: "ok", amount: 1, checked_at: 200 }))));
  }));
  vi.stubGlobal("fetch", fetch);
  const channels = ["a", "a-other", "b", "c", "d", "e"].map((id) => ({
    ...bound, provider: id,
    bound_keys: [{ ...bound.bound_keys![0], account_id: id, base: `https://${id.startsWith("a") ? "a" : id}.test/v1` }],
  }));
  const { result } = renderHook(() => useChannelAccountBalances(channels.map(providerId), channels, "parallel", true, false), { wrapper });
  await waitFor(() => expect([...finish.keys()]).toEqual(["a", "b", "c", "d", "e"]));
  finish.get("a")!();
  await waitFor(() => expect(finish.has("a-other")).toBe(true));
  for (const done of finish.values()) done();
  await waitFor(() => expect([...result.current.values()].every((query) => !query.isPending)).toBe(true));
  expect(fetch).toHaveBeenCalledTimes(6);
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
