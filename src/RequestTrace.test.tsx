import { it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RequestTracePage, TraceTimeline } from "./RequestTrace";
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
it("queries only on submit, preserves errors and final success after retry, and handles no matches", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      calls.push(input);
      return Response.json({
        data:
          new URL(input).searchParams.get("request_id") === "missing"
            ? []
            : [run],
        import: { caught_up: true },
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RequestTracePage
        connection={{
          base: location.origin,
          key: "",
          session: "test",
          account: true,
        }}
        sources={[{ id: "primary", name: "Fugue" }]}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("请求 ID"), "r");
  expect(calls).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "查询请求" }));
  await screen.findAllByText("HTTP 200 · success");
  expect(
    screen.getByText(/INSUFFICIENT_BALANCE · Insufficient account balance/),
  ).toBeVisible();
  expect(screen.getAllByText("second").length).toBeGreaterThan(0);
  expect(screen.queryByText(/1970\/1\/1 08:00:00/)).not.toBeInTheDocument();
  await user.clear(screen.getByLabelText("请求 ID"));
  await user.type(screen.getByLabelText("请求 ID"), "missing");
  await user.click(screen.getByRole("button", { name: "查询请求" }));
  await screen.findByText("未找到该请求的网关记录");
  expect(screen.queryByText("HTTP 200 · success")).not.toBeInTheDocument();
  await waitFor(() => expect(calls).toHaveLength(2));
});
it("marks ambiguous caller IDs instead of inventing a single final outcome", () => {
  render(
    <TraceTimeline run={{ ...run, ambiguous: true }} sourceName="Fugue" />,
  );
  expect(screen.getByText(/重复使用了此请求 ID/)).toBeVisible();
  expect(screen.getByText("无法区分")).toBeVisible();
});
