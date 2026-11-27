import { afterEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { RequestLogsPage, requestLogParams } from "./RequestLogs";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";
import type { RequestLog } from "./RequestLogs";

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  history.replaceState(null, "", location.pathname);
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
      <Tooltip.Provider>
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
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  return userEvent.setup();
}

it("ignores a previously saved ID and exposes a one-click way to clear an active search", async () => {
  const storage = `${location.origin}:logs-user`;
  saveFilters(storage, { ...defaultFilters, window: "1h", search: "stale-id" }, "requests");
  history.replaceState({ workspaceOwner: JSON.stringify([location.origin, "logs-user"]) }, "", "#view=requests&scope=1&window=1h");
  const reads: string[] = [];
  const user = setup(async input => {
    const url = new URL(input);
    if (!url.pathname.endsWith("/request-logs")) return Response.json({ data: [] });
    const search = url.searchParams.get("search") || "";
    reads.push(search);
    return Response.json({ data: search ? [] : [row], models: [] });
  });
  await screen.findByRole("button", { name: row.request_id });
  expect(reads).toEqual([""]);
  await user.click(screen.getByText("定位 ID", { selector: "summary" }));
  await user.type(screen.getByRole("textbox", { name: "搜索请求" }), "missing-id");
  expect(await screen.findByText("当前 ID 搜索没有匹配记录。")).toBeVisible();
  expect(screen.getByText("定位 ID · 已用", { selector: "summary" })).toBeVisible();
  expect(loadFilters(storage, "requests").search).toBe("");
  await user.click(screen.getByRole("button", { name: "清除 ID 搜索" }));
  expect(await screen.findByRole("button", { name: row.request_id })).toBeVisible();
  expect(screen.getByText("定位 ID", { selector: "summary" })).toBeVisible();
  expect(reads).toContain("missing-id");
});

it("can retry an initial log request failure without opening the filters", async () => {
  let reads = 0;
  const user = setup(async input => {
    if (!new URL(input).pathname.endsWith("/request-logs")) return Response.json({ data: [] });
    return ++reads === 1 ? new Response(null, { status: 503 }) : Response.json({ data: [row], models: [] });
  });
  expect(await screen.findByRole("heading", { name: "请求日志暂不可用" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "重试加载" }));
  expect(await screen.findByRole("button", { name: row.request_id })).toBeVisible();
  expect(reads).toBe(2);
});

it.each([
  ["#view=requests", "", "24h"],
  ["#view=requests&scope=1&statusFilter=failed", "failed", defaultFilters.window],
])("ignores a saved status on normal entry but honors explicitly scoped links (%s)", async (hash, result, range) => {
  history.replaceState(null, "", hash);
  const storage = `${location.origin}:logs-user`;
  saveFilters(storage, {...defaultFilters, window: "24h", statusFilter: "failed"}, "requests");
  const reads: URL[] = [];
  const user = setup(async input => {
    const url = new URL(input);
    if (url.pathname.endsWith("/request-logs")) {
      reads.push(url);
      const status = url.searchParams.get("status");
      const rows = [row, {...row, event_id: "e2", status: 502, result: "failed"}];
      return Response.json({ data: status ? rows.filter(log => log.result === status) : rows, models: [] });
    }
    return Response.json({ data: [] });
  });
  await user.click(await screen.findByText(/筛选/, { selector: "summary" }));
  expect(screen.getByLabelText("请求状态筛选")).toHaveValue(result);
  await waitFor(() => {
    expect(reads.at(-1)?.searchParams.get("status") ?? "").toBe(result);
    expect(reads.at(-1)?.searchParams.get("range")).toBe(range);
  });
  expect(await screen.findByText("失败 · HTTP 502")).toBeVisible();
  if (!result) {
    expect(await screen.findByText("成功 · HTTP 200")).toBeVisible();
    await user.selectOptions(screen.getByLabelText("请求状态筛选"), "failed");
    await waitFor(() => expect(reads.at(-1)?.searchParams.get("status")).toBe("failed"));
    await user.click(screen.getByRole("button", { name: "重置筛选" }));
    expect(screen.getByLabelText("请求状态筛选")).toHaveValue("");
    await waitFor(() => expect(reads.at(-1)?.searchParams.get("status")).toBe(""));
  }
});
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
it("renders independent request type and tier labels and preserves legacy column preferences", async () => {
  localStorage.setItem("uni-console:log-columns:v1", JSON.stringify(["request", "result"]));
  setup(async input => {
    const url = new URL(input);
    if (url.pathname.endsWith("/request-logs")) return Response.json({data: [
      {...row, event_id:"fast-compaction", request_type:"compaction", requested_service_tier:"fast"},
      {...row, event_id:"ultra-compaction", request_type:"compaction", requested_service_tier:"ultrafast"},
      {...row, event_id:"standard", request_type:"standard", requested_service_tier:"default"},
      {...row, event_id:"compact-legacy", endpoint:"/v1/responses/compact/"},
      {...row, event_id:"unknown"},
    ], models: []});
    return auxiliary(url) || Response.json({data: []});
  });
  expect(await screen.findByRole("columnheader",{name:"类型"})).toBeVisible();
  await waitFor(() => {
    const cells = Array.from(document.querySelectorAll("td.log-request-type")).map(cell => cell.textContent);
    expect(cells).toEqual(["压缩Fast","压缩Ultrafast","普通","压缩","未记录"]);
  });
});
function traceResult() {
  return {
    import: { caught_up: true },
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
  };
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
it("distinguishes interrupted streams from an HTTP 502 response", async () => {
  const failed = {
    ...row,
    result: "failed",
    status: 502,
    failure_reason: "transport_error",
  };
  setup(async (input) => auxiliary(new URL(input, location.origin)) || Response.json({
    data: [
      { ...failed, response_headers_status: 200 },
      { ...failed, event_id: "e2", request_id: "missing-terminal", response_headers_status: 200, failure_reason: "missing_response_completed" },
      { ...failed, event_id: "e3", response_headers_status: 502 },
      { ...failed, event_id: "e4" },
      { ...failed, event_id: "e5", stream: false, response_headers_status: 200 },
    ],
    models: [row.model],
    next_cursor: "",
    to: row.at_ms,
  }));
  const interrupted = await screen.findAllByText("响应头 HTTP 200 · 流中断");
  expect(interrupted).toHaveLength(2);
  expect(interrupted[0]).toHaveClass("failure");
  expect(screen.getAllByText("失败 · HTTP 502")).toHaveLength(3);
  fireEvent.focus(interrupted[0].parentElement!);
  expect(await screen.findByRole("tooltip")).toHaveTextContent("网关最终判定：502");
});
it("loads logs immediately, filters on the server and can jump to the newest page", async () => {
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
  await user.click(document.querySelector<HTMLElement>(".request-logs-panel .workbench-disclosure:last-child summary")!);
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: "request-two" });
  await user.click(screen.getByRole("button", { name: "查看最新" }));
  await screen.findByRole("button", { name: "request-one" });
  expect(calls.at(-1)?.searchParams.get("cursor")).toBe("");
  expect(document.querySelector(".request-logs-panel .workbench-disclosure:last-child")).not.toHaveAttribute("open");
  await user.click(document.querySelector<HTMLElement>(".request-logs-panel .workbench-disclosure:last-child summary")!);
  await user.selectOptions(screen.getByLabelText("时间范围筛选"), "24h");
  await user.selectOptions(screen.getByLabelText("uni-api 来源"), "fugue");
  await user.selectOptions(screen.getByLabelText("API key 筛选"), "fugue::key");
  const rate = await screen.findByText(/90.0%/);
  expect(rate).toBeVisible();
  expect(rate.closest(".select-field")).toContainElement(screen.getByLabelText("API key 筛选"));
  await user.selectOptions(
    screen.getByLabelText("模型筛选"),
    "historical-model",
  );
  await user.selectOptions(screen.getByLabelText("端点筛选"), "/v1/messages");
  await user.selectOptions(screen.getByLabelText("流式状态筛选"), "false");
  await user.selectOptions(screen.getByLabelText("请求状态筛选"), "failed");
  await user.click(document.querySelector<HTMLElement>(".request-logs-panel .workbench-disclosure:first-child summary")!);
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
  await user.click(document.querySelector<HTMLElement>(".request-logs-panel .workbench-disclosure:last-child summary")!);
  await user.click(screen.getByRole("button", { name: "重置筛选" }));
  expect(screen.getByLabelText("时间范围筛选")).toHaveValue("15m");
  expect(screen.getByLabelText("模型筛选")).toHaveValue("");
  expect(screen.getByLabelText("搜索请求")).toHaveValue("");
  expect(await screen.findByRole("button", {name:"request-one"})).toBeVisible();
});
it("opens the existing timeline in a scoped modal and returns to the same log page", async () => {
  const calls: URL[] = [];
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    calls.push(url);
    if (url.pathname.endsWith("/request-trace"))
      return Response.json(traceResult());
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

it("prefetches only the intended row, shares in-flight work, and reopens instantly", async () => {
  const traces: URL[] = [];
  let finish!: (response: Response) => void;
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    if (url.pathname.endsWith("/request-trace")) {
      traces.push(url);
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    }
    return (
      auxiliary(url) ||
      Response.json({
        data: [
          row,
          {
            ...row,
            event_id: "e2",
            request_id: "another",
            instance_id: "second",
          },
        ],
        models: [row.model],
        next_cursor: "",
        to: row.at_ms,
      })
    );
  });
  const button = await screen.findByRole("button", { name: row.request_id });
  expect(traces).toHaveLength(0);
  await user.hover(button);
  await waitFor(() => expect(traces).toHaveLength(1));
  await user.click(button);
  expect(traces).toHaveLength(1);
  expect(screen.getByRole("status", {name:"加载请求详情"})).toBeVisible();
  await act(async () => finish(Response.json(traceResult())));
  await screen.findByRole("heading", { name: "请求成功" });
  await user.click(screen.getByRole("button", { name: "关闭请求详情" }));
  await user.click(button);
  expect(screen.getByRole("heading", { name: "请求成功" })).toBeVisible();
  expect(screen.queryByRole("status", {name:"加载请求详情"})).not.toBeInTheDocument();
  expect(traces).toHaveLength(1);
  await user.click(screen.getByRole("button", { name: "刷新记录" }));
  await waitFor(() => expect(traces).toHaveLength(2));
  await act(async () => finish(Response.json(traceResult())));
  await user.click(screen.getByRole("button", { name: "关闭请求详情" }));
  fireEvent.focus(screen.getByRole("button", { name: "another" }));
  await waitFor(() => expect(traces).toHaveLength(3));
  expect(traces[2].searchParams.get("instance_id")).toBe("second");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  await act(async () => finish(Response.json(traceResult())));
});

it("a failed intent prefetch does not prevent an explicit click from retrying", async () => {
  let attempts = 0;
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    if (url.pathname.endsWith("/request-trace")) {
      attempts++;
      return attempts === 1
        ? new Response("unavailable", { status: 503 })
        : Response.json(traceResult());
    }
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
  const button = await screen.findByRole("button", { name: row.request_id });
  await user.hover(button);
  await waitFor(() => expect(attempts).toBe(1));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  await user.click(button);
  await screen.findByRole("heading", { name: "请求成功" });
  expect(attempts).toBe(2);
});

it("keeps the visible rows mounted until refresh succeeds and preserves them on failure", async () => {
  let reads = 0;
  let finish!: (response: Response) => void;
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    if (url.pathname.endsWith("/request-trace"))
      return Response.json(traceResult());
    reads++;
    if (reads > 1)
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    return Response.json({
      data: [row],
      models: [row.model],
      next_cursor: "next",
      to: row.at_ms,
    });
  });
  const original = await screen.findByRole("button", { name: row.request_id });
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  expect(original).toBeVisible();
  expect(screen.getByRole("table")).toBeVisible();
  expect(screen.queryByText("正在读取请求日志…")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "刷新日志" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled();
  await act(async () => finish(new Response("unavailable", { status: 503 })));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "刷新失败，保留当前日志",
  );
  expect(original).toBeVisible();
  expect(screen.getByRole("button", { name: "刷新日志" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  expect(original).toBeVisible();
  await act(async () =>
    finish(
      Response.json({
        data: [{ ...row, event_id: "new", request_id: "latest" }],
        models: [row.model],
        next_cursor: "",
        to: row.at_ms + 1000,
      }),
    ),
  );
  await screen.findByRole("button", { name: "latest" });
  expect(original).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(reads).toBe(3); // Prepared data does not cause a duplicate request on display.
});

it("refreshes the visible historical page without resetting the cursor", async () => {
  let reads = 0;
  let finish!: (response: Response) => void;
  let refreshedCursor: string | null = null;
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    reads++;
    if (reads > 2) {
      refreshedCursor = url.searchParams.get("cursor");
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    }
    const next = url.searchParams.get("cursor") === "next";
    return Response.json({
      data: [
        {
          ...row,
          event_id: next ? "two" : "one",
          request_id: next ? "page-two" : "page-one",
        },
      ],
      models: [row.model],
      next_cursor: "next",
      to: row.at_ms,
    });
  });
  await screen.findByRole("button", { name: "page-one" });
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: "page-two" });
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  expect(screen.getByRole("button", { name: "page-two" })).toBeVisible();
  expect(screen.getByText(/第 2 页/)).toBeVisible();
  await act(async () => finish(new Response("unavailable", { status: 503 })));
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "page-two" })).toBeVisible();
  expect(screen.getByText(/第 2 页/)).toBeVisible();
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  await act(async () =>
    finish(
      Response.json({
        data: [{ ...row, event_id: "new", request_id: "updated-page-two" }],
        models: [row.model],
        next_cursor: "new-cursor",
        to: row.at_ms + 1000,
      }),
    ),
  );
  await screen.findByRole("button", { name: "updated-page-two" });
  expect(screen.getByText(/第 2 页/)).toBeVisible();
  expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
  expect(refreshedCursor).toBe("next");
  expect(reads).toBe(4);
});

it("does not apply an older refresh after the user changes filters or pages", async () => {
  let reads = 0;
  let finishOld!: (response: Response) => void;
  const user = setup(async (input) => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    reads++;
    if (reads === 2)
      return new Promise<Response>((resolve) => {
        finishOld = resolve;
      });
    const filtered = url.searchParams.get("model") === row.model;
    const next = url.searchParams.get("cursor") === "next";
    return Response.json({
      data: [
        {
          ...row,
          event_id: `e${reads}`,
          request_id: filtered
            ? next
              ? "filtered-two"
              : "filtered-one"
            : "original",
        },
      ],
      models: [row.model],
      next_cursor: "next",
      to: row.at_ms,
    });
  });
  await screen.findByRole("button", { name: "original" });
  await user.click(screen.getByRole("button", { name: "刷新日志" }));
  await user.selectOptions(screen.getByLabelText("模型筛选"), row.model);
  await screen.findByRole("button", { name: "filtered-one" });
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: "filtered-two" });
  await act(async () =>
    finishOld(
      Response.json({
        data: [row],
        models: [row.model],
        next_cursor: "",
        to: row.at_ms + 1000,
      }),
    ),
  );
  expect(screen.getByRole("button", { name: "filtered-two" })).toBeVisible();
  expect(screen.getByText(/第 2 页/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: row.request_id }),
  ).not.toBeInTheDocument();
});

it("navigates using the visible cursor while a same-filter refresh is pending", async () => {
  let firstPageReads = 0;
  let finish!: (response: Response) => void;
  const user = setup(async input => {
    const url = new URL(input, location.origin);
    const aux = auxiliary(url);
    if (aux) return aux;
    const next = url.searchParams.get("cursor") === "next";
    if (!next && ++firstPageReads > 1)
      return new Promise<Response>(resolve => { finish = resolve; });
    return Response.json({
      data: [{...row, request_id: next ? "next-page" : "first-page"}],
      models: [row.model],
      next_cursor: next ? "" : "next",
      to: row.at_ms,
    });
  });
  await screen.findByRole("button", {name: "first-page"});
  await user.click(screen.getByRole("button", {name: "刷新日志"}));
  await waitFor(() => expect(finish).toBeDefined());
  await user.click(screen.getByRole("button", {name: "下一页"}));
  await screen.findByRole("button", {name: "next-page"});
  await act(async () => finish(Response.json({data: [row], next_cursor: "", to: row.at_ms + 1000})));
  expect(screen.getByRole("button", {name: "next-page"})).toBeVisible();
  expect(screen.getByText(/第 2 页/)).toBeVisible();
});
