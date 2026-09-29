import { it, expect, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RequestTraceDialog, TraceTimeline } from "./RequestTrace";
import type { TraceRun, TraceEvent } from "./RequestTrace";
const events: Partial<TraceEvent>[] = [
  {
    event_id: "start",
    kind: "trace",
    stage: "request_received",
    at_ms: 100000,
    detail: {},
    transport: {},
  },
  {
    event_id: "one",
    kind: "billing",
    attempt_id: "r-r1",
    provider: "first",
    status: 403,
    at_ms: 100100,
    detail: {
      error: {
        error_code: "INSUFFICIENT_BALANCE",
        error_message: "Insufficient account balance",
      },
    },
    transport: {},
  },
  {
    event_id: "two",
    kind: "dispatch",
    attempt_id: "r-r2",
    provider: "second",
    at_ms: 100200,
    dispatch_ms: 200,
    detail: {},
    transport: {},
  },
  {
    event_id: "end",
    kind: "request",
    provider: "second",
    at_ms: 100300,
    status: 200,
    outcome: "success",
    duration_ms: 300,
    model: "gpt-6-sol",
    detail: {},
    transport: {},
  },
];
const run: TraceRun = {
  source_id: "primary",
  instance_id: "instance",
  request_id: "r",
  ambiguous: false,
  events: events.map((e) => ({
    event_id: "",
    kind: "",
    stage: "",
    at_ms: 0,
    started_ms: 0,
    attempt_id: "",
    provider: "",
    model: "",
    upstream_model: "",
    endpoint: "",
    stream: false,
    outcome: "",
    status: 0,
    terminal_kind: "",
    failure_reason: "",
    response_completed: null,
    duration_ms: null,
    dispatch_ms: null,
    response_created_ms: null,
    first_text_ms: null,
    detail: {},
    transport: {},
    ...e,
  })),
};
it("loads a scoped request in a modal, preserves retry errors and refreshes", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      calls.push(input);
      return Response.json({ data: [run], import: { caught_up: true } });
    }),
  );
  const close = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RequestTraceDialog
        connection={{
          base: location.origin,
          key: "",
          session: "test",
          account: true,
        }}
        sources={[{ id: "primary", name: "Fugue" }]}
        requestId="r"
        sourceId="primary"
        instanceId="instance"
        onClose={close}
      />
    </QueryClientProvider>,
  );
  await screen.findByRole("heading", { name: "请求成功" });
  const url = new URL(calls[0]);
  expect(url.searchParams.get("source_id")).toBe("primary");
  expect(url.searchParams.get("instance_id")).toBe("instance");
  expect(
    screen.getAllByText(/账号余额不足 · Insufficient account balance/)[0],
  ).toBeVisible();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "刷新记录" }));
  await waitFor(() => expect(calls).toHaveLength(2));
  await user.click(screen.getByRole("button", { name: "关闭请求详情" }));
  expect(close).toHaveBeenCalledOnce();
});
it("marks ambiguous caller IDs instead of inventing a single final outcome", () => {
  render(
    <TraceTimeline run={{ ...run, ambiguous: true }} sourceName="Fugue" />,
  );
  expect(screen.getByText(/重复使用了此请求 ID/)).toBeVisible();
  expect(screen.getByText("无法区分")).toBeVisible();
});

it("coalesces attempt events into a waterfall, uses readable names, and opens channel stage details", async () => {
  const user = userEvent.setup();
  const provider = "sub2api-b58737622de5f8c5a0633b12";
  const item = {
    ...run,
    channels: {
      [provider]: {
        name: "xrelayai-0.15",
        site_name: "xrelayai",
        group_name: "纯Pro号池",
        group_id: 5,
        rate: 0.15,
        dashboard_url: "https://xrelayai.com/dashboard",
      },
    },
    events: [
      { ...run.events[0] },
      {
        ...run.events[2],
        event_id: "d1",
        attempt_id: "r-r1",
        provider,
        at_ms: 100010,
      },
      { ...run.events[1], event_id: "b1", provider, at_ms: 100100 },
      {
        ...run.events[1],
        event_id: "a1",
        kind: "attempt",
        provider,
        at_ms: 100100,
        duration_ms: 90,
        outcome: "failed/upstream_http_403",
        detail: {},
      },
      {
        ...run.events[3],
        status: 403,
        outcome: "failed/other",
        duration_ms: 100,
        at_ms: 100100,
      },
    ],
  };
  render(<TraceTimeline run={item} sourceName="Fugue" />);
  const chart = screen.getByRole("region", { name: "渠道请求时间线" });
  expect(within(chart).getAllByRole("button")).toHaveLength(2);
  expect(screen.getByText("纯Pro号池")).toBeVisible();
  for (const el of screen.getAllByText(provider)) expect(el).not.toBeVisible();
  await user.click(
    within(chart).getByRole("button", {
      name: "1xrelayai · 0.15×",
    }),
  );
  const drawer = screen.getByRole("dialog");
  expect(
    within(drawer).getByRole("heading", { name: "xrelayai · 0.15×" }),
  ).toBeVisible();
  expect(
    within(drawer).getByRole("link", { name: "打开站点控制台" }),
  ).toHaveAttribute("href", "https://xrelayai.com/dashboard");
  expect(
    within(drawer).getByRole("heading", { name: "阶段时间线" }),
  ).toBeVisible();
  expect(within(drawer).getByText("发起渠道请求")).toBeVisible();
  await user.click(
    within(drawer).getByRole("button", { name: "关闭尝试详情" }),
  );
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("labels legacy origins, hides opaque names and tolerates empty records", () => {
  const legacy = {
    ...run,
    events: run.events
      .slice(1)
      .map((e) => ({ ...e, provider: "sub2api-deadbeef" })),
  };
  const view = render(<TraceTimeline run={legacy} sourceName="Fugue" />);
  expect(screen.getByText("首条可见记录")).toBeVisible();
  expect(screen.getByText(/历史记录缺少入口时间/)).toBeVisible();
  expect(
    screen.getAllByRole("button", { name: /未关联渠道 1/ }).length,
  ).toBeGreaterThan(0);
  expect(
    screen.queryByRole("button", { name: /sub2api-deadbeef/ }),
  ).not.toBeInTheDocument();
  view.unmount();
  render(<TraceTimeline run={{ ...run, events: [] }} sourceName="Fugue" />);
  expect(screen.getByText("没有可绘制的渠道尝试记录。")).toBeVisible();
});
