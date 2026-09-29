import { expect, it, vi } from "vitest";
import { createElement } from "react";
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  readKeyRequestStats,
  requestRateLabel,
  useKeyRequestStats,
} from "./keyRequestStats";

it("loads every key in one request scoped to source, model and time", async () => {
  const fetch = vi.fn(
    async (_input: RequestInfo | URL) =>
      new Response(JSON.stringify({ data: [], total: { requests: 0 } })),
  );
  vi.stubGlobal("fetch", fetch);
  await readKeyRequestStats(
    {
      base: location.origin,
      key: "",
      session: "test",
      account: true,
      sourceId: "fugue",
    },
    {
      range: "24h",
      model: "gpt-6-sol",
      endpoint: "/v1/responses",
      stream: "true",
    },
    new AbortController().signal,
  );
  const url = new URL(String(fetch.mock.calls[0]?.[0]));
  expect(url.pathname).toBe("/analytics/v1/key-request-stats");
  expect(Object.fromEntries(url.searchParams)).toEqual({
    range: "24h",
    model: "gpt-6-sol",
    source_id: "fugue",
    endpoint: "/v1/responses",
    stream: "true",
  });
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("distinguishes no data, failed-only requests, incomplete history and rounding", () => {
  expect(requestRateLabel()).toBe("暂无已完成请求");
  expect(requestRateLabel(undefined, true)).toBe("暂无已同步请求");
  expect(
    requestRateLabel({
      requests: 3,
      success: 2,
      failed: 1,
      success_rate: 2 / 3,
    }),
  ).toBe("请求成功率 66.7%（2/3）");
  expect(
    requestRateLabel({ requests: 1, success: 0, failed: 1, success_rate: 0 }),
  ).toBe("请求成功率 0.0%（0/1）");
  expect(
    requestRateLabel(
      { requests: 10000, success: 9999, failed: 1, success_rate: 0.9999 },
      true,
    ),
  ).toBe("请求成功率 <100%（9999/10000） · 部分记录");
});

it("keeps source/key identities separate and never shows another model's cached rate", async () => {
  let finish: (value: Response) => void = () => {};
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      if (new URL(String(input)).searchParams.get("model") === "other")
        return new Promise<Response>((resolve) => {
          finish = resolve;
        });
      return new Response(
        JSON.stringify({
          data: [
            {
              source_id: "fugue",
              key_id: "same",
              requests: 1,
              success: 1,
              failed: 0,
              success_rate: 1,
            },
            {
              source_id: "digitalocean",
              key_id: "same",
              requests: 1,
              success: 0,
              failed: 1,
              success_rate: 0,
            },
          ],
          total: { requests: 2, success: 1, failed: 1, success_rate: 0.5 },
          import: { caught_up: true },
        }),
      );
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const connection = {
    base: location.origin,
    key: "",
    session: "test",
    account: true,
    sourceId: "all",
  };
  const { result, rerender, unmount } = renderHook(
    ({ model }) => useKeyRequestStats(connection, { range: "1h", model }, true),
    {
      initialProps: { model: "" },
      wrapper: ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client }, children),
    },
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(result.current.label({ key_id: "fugue::same" })).toContain(
    "100.0%（1/1）",
  );
  expect(
    result.current.label({ key_id: "same", source_id: "digitalocean" }),
  ).toContain("0.0%（0/1）");
  expect(result.current.label()).toContain("50.0%（1/2）");
  rerender({ model: "other" });
  expect(result.current.label({ key_id: "fugue::same" })).toBe(
    "请求成功率加载中",
  );
  finish(
    new Response(
      JSON.stringify({
        data: [],
        total: { requests: 0, success: 0, failed: 0, success_rate: null },
      }),
    ),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(result.current.label({ key_id: "fugue::same" })).toBe(
    "暂无已完成请求",
  );
  unmount();
  client.clear();
});
