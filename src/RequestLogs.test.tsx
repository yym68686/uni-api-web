import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RequestLogsPage, requestLogParams } from "./RequestLogs";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";
import type { RequestLog } from "./RequestLogs";

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});
const row: RequestLog = {
  event_id: "e1",
  source_id: "fugue",
  instance_id: "instance",
  request_id: "request-one",
  trace_id: "trace-one",
  key_id: "key",
  model: "gpt-6-sol",
  endpoint: "/v1/responses",
  stream: true,
  at_ms: 1789999999000,
  started_ms: null,
  status: 200,
  outcome: "success",
  result: "success",
  failure_reason: "",
  duration_ms: 300,
  dispatch_ms: 10,
  response_created_ms: 100,
  first_text_ms: 150,
  input_tokens: 100,
  output_tokens: 20,
  cache_read_tokens: 0,
};
function setup(fetcher: (input: string) => Promise<Response>) {
  vi.stubGlobal("fetch", vi.fn(fetcher));
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RequestLogsPage
        connection={{
          base: location.origin,
          key: "",
          session: "logs-user",
          account: true,
        }}
        sources={[
          { id: "fugue", name: "Fugue" },
          { id: "do", name: "DigitalOcean" },
        ]}
      />
    </QueryClientProvider>,
  );
  return userEvent.setup();
}
function auxiliary(url: URL) {
  if (url.pathname.endsWith("/api-keys"))
    return Response.json({
      can_inspect_all: true,
      data: [
        {
          key_id: "fugue::key",
          source_id: "fugue",
          source_name: "Fugue",
          position: 1,
          prefix: "sk-…test",
        },
      ],
    });
  if (url.pathname.endsWith("/key-request-stats"))
    return Response.json({
      data: [
        {
          source_id: "fugue",
          key_id: "key",
          requests: 10,
          success: 9,
          failed: 1,
        },
      ],
      total: { requests: 10, success: 9, failed: 1 },
      import: { caught_up: true },
    });
  return undefined;
}
it("scopes opaque key IDs by source and keeps log preferences independent", () => {
  const params = requestLogParams(
    { ...defaultFilters, sourceId: "fugue", keyId: "fugue::key" },
    "",
  );
  expect(params.get("source_id")).toBe("fugue");
  expect(params.get("key_id")).toBe("key");
  expect(() =>
    requestLogParams(
      { ...defaultFilters, sourceId: "do", keyId: "fugue::key" },
      "",
    ),
  ).toThrow();
  saveFilters(
    "base",
    { ...defaultFilters, model: "channel-model" },
    "channels",
  );
  saveFilters(
    "base",
    { ...defaultFilters, model: "balance-model" },
    "balances",
  );
  expect(loadFilters("base", "requests")).toEqual(defaultFilters);
  saveFilters(
    "base",
    { ...defaultFilters, model: "log-model", statusFilter: "failed" },
    "requests",
  );
  expect(loadFilters("base", "channels").model).toBe("channel-model");
  expect(loadFilters("base", "balances").model).toBe("balance-model");
  expect(loadFilters("base", "requests").statusFilter).toBe("failed");
});
it("loads logs immediately, filters on the server, paginates and refreshes to the newest page", async () => {
  const calls: URL[] = [];
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    calls.push(url);
    const next = url.searchParams.get("cursor") === "next";
    return Response.json({
      data: [
        {
          ...row,
          event_id: next ? "e2" : "e1",
          request_id: next ? "request-two" : "request-one",
        },
      ],
      models: ["gpt-6-sol", "historical-model"],
      next_cursor: next ? "" : "next",
      from: 1789990000000,
      to: 1790000000000,
    });
  });
  await screen.findByRole("button", { name: "request-one" });
  expect(screen.getByLabelText("API key 筛选")).toHaveTextContent("90.0%");
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: "request-two" });
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  await screen.findByRole("button", { name: "request-one" });
  expect(calls.at(-1)?.searchParams.get("cursor")).toBe("");
  await user.selectOptions(screen.getByLabelText("时间范围筛选"), "24h");
  await user.selectOptions(screen.getByLabelText("uni-api 来源"), "fugue");
  await user.selectOptions(screen.getByLabelText("API key 筛选"), "fugue::key");
  await user.selectOptions(
    screen.getByLabelText("模型筛选"),
    "historical-model",
  );
  await user.selectOptions(screen.getByLabelText("端点筛选"), "/v1/messages");
  await user.selectOptions(screen.getByLabelText("流式状态筛选"), "false");
  await user.selectOptions(screen.getByLabelText("请求状态筛选"), "failed");
  await user.type(screen.getByLabelText("搜索请求"), "exact-id");
  await waitFor(() =>
    expect(calls.at(-1)?.searchParams.get("search")).toBe("exact-id"),
  );
  expect(Object.fromEntries(calls.at(-1)!.searchParams)).toMatchObject({
    range: "24h",
    source_id: "fugue",
    key_id: "key",
    model: "historical-model",
    endpoint: "/v1/messages",
    stream: "false",
    status: "failed",
  });
  await user.click(screen.getByRole("button", { name: "重置筛选" }));
  await waitFor(() => expect(calls.at(-1)?.searchParams.get("model")).toBe(""));
  expect(screen.getByLabelText("时间范围筛选")).toHaveValue("15m");
});
it("opens the existing timeline in a scoped modal and returns to the same log page", async () => {
  const calls: URL[] = [];
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    calls.push(url);
    if (url.pathname.endsWith("/request-trace"))
      return Response.json({
        data: [
          {
            source_id: "fugue",
            instance_id: "instance",
            request_id: row.request_id,
            ambiguous: false,
            events: [
              {
                ...row,
                kind: "request",
                stage: "",
                provider: "",
                upstream_model: "",
                response_completed: true,
                attempt_id: "",
                detail: {},
                transport: {},
              },
            ],
          },
        ],
      });
    return Response.json({
      data: [row],
      models: [row.model],
      next_cursor: "",
      to: row.at_ms,
    });
  });
  await user.click(await screen.findByRole("button", { name: row.request_id }));
  const modal = await screen.findByRole("dialog", { name: "请求详情" });
  await within(modal).findByRole("heading", { name: "请求成功" });
  const trace = calls.find((url) => url.pathname.endsWith("/request-trace"))!;
  expect(trace.searchParams.get("source_id")).toBe("fugue");
  expect(trace.searchParams.get("instance_id")).toBe("instance");
  await user.click(within(modal).getByRole("button", { name: "关闭请求详情" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: row.request_id })).toBeVisible();
});
it("shows logs even when the live key directory fails", async () => {
  setup(async (input) => {
    const url = new URL(input, location.origin);
    if (url.pathname.endsWith("/api-keys"))
      return new Response("unavailable", { status: 503 });
    return (
      auxiliary(url) ||
      Response.json({
        data: [row],
        models: [row.model],
        next_cursor: "",
        to: row.at_ms,
      })
    );
  });
  await screen.findByRole("button", { name: row.request_id });
  await screen.findByText("API key 目录暂不可用；请求日志仍可浏览。");
});
