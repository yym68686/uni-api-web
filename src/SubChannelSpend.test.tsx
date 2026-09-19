import { expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { SubChannelSpendValue, useSubChannelSpend } from "./SubChannelSpend";
import type { SubChannelSpend } from "./SubChannelSpend";
import type { InstalledChannel } from "./sub2apiImports";
import { providerId } from "./format";

const first = {
  source_id: "source-a",
  provider: "sub2api-first",
  account_id: "account",
  group_id: 7,
} as InstalledChannel;
const second = {
  ...first,
  source_id: "source-b",
  provider: "sub2api-second",
} as InstalledChannel;
const providers = [providerId(first), providerId(second)];
const imports = [first, second];
const receipt: SubChannelSpend = {
  status: "complete",
  actual_cost_usd: 0.00008,
  requests: 2,
  from: 1,
  to: 1800000000,
  checked_at: 1800000000,
  key_id: 42,
  scope: "sub2api_business_key",
};
function Fixture({
  window,
  cutoff = 1800000000,
}: {
  window: string;
  cutoff?: number;
}) {
  const queries = useSubChannelSpend({
    providers,
    imports,
    session: "fixture",
    window,
    to: cutoff,
    refresh: 0,
    auto: false,
    enabled: true,
  });
  return (
    <>
      {providers.map((id) => (
        <div key={id}>
          <SubChannelSpendValue query={queries.get(id)} />
        </div>
      ))}
    </>
  );
}
it("shares one business-key query across imported bindings and scopes each time selection", async () => {
  const fetch = vi.fn(async (input: string) => {
    const url = new URL(input);
    return new Response(
      JSON.stringify({
        ...receipt,
        actual_cost_usd: url.searchParams.get("range") === "1h" ? 0.00008 : 5,
      }),
    );
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const ui = (window: string) => (
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <Fixture window={window} />
      </Tooltip.Provider>
    </QueryClientProvider>
  );
  const app = render(ui("1h"));
  expect(await screen.findAllByText("$0.00008")).toHaveLength(2);
  expect(fetch).toHaveBeenCalledTimes(1);
  const url = new URL(fetch.mock.calls[0][0]);
  expect(url.pathname).toBe(
    "/analytics/v1/sub2api/accounts/account/groups/7/spend",
  );
  expect(url.searchParams.get("range")).toBe("1h");
  expect(url.searchParams.get("to")).toBe("1800000000");
  app.rerender(ui("24h"));
  await waitFor(() => expect(screen.getAllByText("$5.00")).toHaveLength(2));
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(new URL(fetch.mock.calls[1][0]).searchParams.get("range")).toBe("24h");
});

it("never presents partial, unsupported or failed billing as zero", () => {
  const client = new QueryClient();
  const app = render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <SubChannelSpendValue
          query={{
            data: { ...receipt, status: "pending", actual_cost_usd: null },
            isPending: false,
            isError: false,
          }}
        />
        <SubChannelSpendValue
          query={{
            data: { ...receipt, status: "error", actual_cost_usd: null },
            isPending: false,
            isError: false,
          }}
        />
        <SubChannelSpendValue
          query={{
            data: { ...receipt, status: "unsupported", actual_cost_usd: null },
            isPending: false,
            isError: false,
          }}
        />
        <SubChannelSpendValue
          query={{
            data: { ...receipt, status: "complete", actual_cost_usd: 0 },
            isPending: false,
            isError: false,
          }}
        />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  expect(screen.getByText("同步账单")).toBeVisible();
  expect(screen.getByText("查询失败")).toBeVisible();
  expect(screen.getByText("未确认")).toBeVisible();
  expect(screen.getAllByText("$0.00")).toHaveLength(1);
  app.unmount();
});

it("attributes by caller and all analytic filters, without leaking a cached total across selections", async () => {
  const { useScopedChannelSpend } = await import("./SubChannelSpend");
  const { rowId } = await import("./format");
  const row = {
    source_id: "source-a",
    provider: "shared",
    model: "gpt-6-astra",
    upstream_model: "gpt-6-astra",
    endpoint: "/v1/responses",
    stream: true,
  } as import("./types").Channel;
  const fetch = vi.fn(async (input: string) => {
    const q = new URL(input).searchParams;
    return new Response(
      JSON.stringify({
        data: [
          {
            ...row,
            scope: "matched_requests",
            status: q.get("key_id") === "caller-old" ? "unmatched" : "complete",
            actual_cost_usd:
              q.get("key_id") === "caller-a"
                ? 1
                : q.get("key_id") === "caller-b"
                  ? 2
                  : null,
            from: 100,
            to: 200,
          },
        ],
      }),
    );
  });
  vi.stubGlobal("fetch", fetch);
  function Scoped({ caller }: { caller: string }) {
    const queries = useScopedChannelSpend({
      rows: [row],
      session: "test",
      sourceId: "",
      keyId: "source-a::" + caller,
      model: row.model,
      endpoint: row.endpoint,
      stream: "true",
      from: 100,
      to: 200,
      refresh: 0,
      auto: false,
      enabled: true,
    });
    return <SubChannelSpendValue query={queries.get(rowId(row))} />;
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const ui = (key: string) => (
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <Scoped caller={key} />
      </Tooltip.Provider>
    </QueryClientProvider>
  );
  const app = render(ui("caller-a"));
  expect(await screen.findByText("$1.00")).toBeVisible();
  expect(
    Object.fromEntries(new URL(fetch.mock.calls[0][0]).searchParams),
  ).toEqual({
    source_id: "source-a",
    key_id: "caller-a",
    model: "gpt-6-astra",
    endpoint: "/v1/responses",
    stream: "true",
    from: "100",
    to: "200",
  });
  app.rerender(ui("caller-b"));
  expect(await screen.findByText("$2.00")).toBeVisible();
  expect(screen.queryByText("$1.00")).toBeNull();
  app.rerender(ui("caller-old"));
  expect(await screen.findByText("无法归属")).toBeVisible();
  expect(screen.queryByText("$0.00")).toBeNull();
});

it("continues polling delayed receipts alongside missing history with auto refresh disabled", async () => {
  const { useScopedChannelSpend } = await import("./SubChannelSpend");
  const { rowId } = await import("./format");
  const row = {
    source_id: "s",
    provider: "p",
    model: "m",
    upstream_model: "m",
    endpoint: "all",
    stream: null,
  } as import("./types").Channel;
  const initial = {
    ...row,
    scope: "matched_requests",
    status: "unmatched",
    actual_cost_usd: null,
    matched_cost_usd: 0.25,
    total_attempts: 3,
    matched_attempts: 1,
    missing_identifiers: 1,
    from: 100,
    to: 200,
  };
  const done = {
    ...initial,
    matched_cost_usd: 1,
    matched_attempts: 2,
    pending_attempts: 0,
    refreshing: false,
  };
  vi.useFakeTimers();
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ data: [initial] })))
    .mockImplementation(
      async () => new Response(JSON.stringify({ data: [done] })),
    );
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Fixture() {
    const result = useScopedChannelSpend({
      rows: [row],
      session: "mixed",
      sourceId: "s",
      keyId: "caller",
      model: "m",
      endpoint: "all",
      stream: "all",
      from: 100,
      to: 200,
      refresh: 0,
      auto: false,
      enabled: true,
    });
    return <SubChannelSpendValue query={result.get(rowId(row))} />;
  }
  const app = render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <Fixture />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  try {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    expect(screen.getByText("≥$0.25")).toBeVisible();
    expect(screen.getByText("部分 · 1/3")).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5050);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(screen.getByText("≥$1.00")).toBeVisible();
    expect(screen.getByText("部分 · 2/3")).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("$1.00")).toBeNull();
  } finally {
    app.unmount();
    client.clear();
    vi.useRealTimers();
  }
});

it("explains exact missing categories and distinguishes verified partial spend from the total", async () => {
  const data: SubChannelSpend = {
    ...receipt,
    scope: "matched_requests",
    status: "unmatched",
    actual_cost_usd: null,
    matched_cost_usd: 1.25,
    total_attempts: 10,
    matched_attempts: 3,
    missing_correlation_attempts: 4,
    missing_response_identifiers: 2,
    missing_response_statuses: { "524": 2 },
    unbound_attempts: 1,
    pending_attempts: 0,
  };
  const app = render(
    <Tooltip.Provider>
      <SubChannelSpendValue
        query={{ data, isPending: false, isError: false }}
      />
    </Tooltip.Provider>,
  );
  const amount = screen.getByText("≥$1.25");
  fireEvent.focus(amount.closest(".tip-target")!);
  expect(await screen.findByRole("tooltip")).toHaveTextContent(
    "缺少关联事实 4 次",
  );
  expect(screen.getByRole("tooltip")).toHaveTextContent("HTTP 524：2");
  expect(screen.getByRole("tooltip")).toHaveTextContent("账号关联未确认 1 次");
  expect(screen.getByRole("tooltip")).toHaveTextContent("不是完整消费");
  app.rerender(
    <Tooltip.Provider>
      <SubChannelSpendValue
        query={{
          data: {
            ...data,
            status: "no_records",
            matched_attempts: 0,
            total_attempts: 0,
          },
          isPending: false,
          isError: false,
        }}
      />
    </Tooltip.Provider>,
  );
  expect(screen.queryByText("无法归属")).toBeNull();
  expect(screen.queryByText("$0.00")).toBeNull();
  expect(screen.getByText("—")).toBeVisible();
});

it("distinguishes completed ledger scans from downloads and rechecks absent bills slowly", async () => {
  const { useScopedChannelSpend } = await import("./SubChannelSpend");
  const { rowId } = await import("./format");
  const row = {
    source_id: "s",
    provider: "p",
    model: "m",
    upstream_model: "m",
    endpoint: "all",
    stream: null,
  } as import("./types").Channel;
  const absent = {
    ...row,
    scope: "matched_requests",
    status: "unmatched",
    actual_cost_usd: null,
    matched_cost_usd: 1,
    total_attempts: 2,
    matched_attempts: 1,
    missing_identifiers: 0,
    absent_receipt_attempts: 1,
    absent_receipt_statuses: { "429": 1 },
    pending_attempts: 0,
    refreshing: false,
    from: 100,
    to: 200,
    checked_at: 300,
  };
  const complete = {
    ...absent,
    status: "complete",
    actual_cost_usd: 1.1,
    matched_cost_usd: 1.1,
    matched_attempts: 2,
    absent_receipt_attempts: 0,
  };
  vi.useFakeTimers();
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ data: [absent] })))
    .mockImplementation(
      async () => new Response(JSON.stringify({ data: [complete] })),
    );
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Fixture() {
    const result = useScopedChannelSpend({
      rows: [row],
      session: "absent",
      sourceId: "s",
      keyId: "caller",
      model: "m",
      endpoint: "all",
      stream: "all",
      from: 100,
      to: 200,
      refresh: 0,
      auto: false,
      enabled: true,
    });
    return <SubChannelSpendValue query={result.get(rowId(row))} />;
  }
  const app = render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <Fixture />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  try {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    const amount = screen.getByText("≥$1.00");
    fireEvent.focus(amount.closest(".tip-target")!);
    expect(screen.getByRole("tooltip")).toHaveTextContent(
      "已完成相应时间段的账单同步，但未找到 1 次请求的账单",
    );
    expect(screen.getByRole("tooltip")).toHaveTextContent("HTTP 429：1");
    expect(screen.getByRole("tooltip")).not.toHaveTextContent(
      "账单同步尚未覆盖",
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(46000);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(screen.getByText("$1.10")).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
  } finally {
    app.unmount();
    client.clear();
    vi.useRealTimers();
  }
});
