import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import userEvent from "@testing-library/user-event";
import {
  BalanceModelMetricCells,
  useBalanceModelMetrics,
  balanceModelMetricID,
} from "./BalanceModelMetrics";
import type { BalanceModelMetric } from "./BalanceModelMetrics";
afterEach(() => vi.unstubAllGlobals());
const timing = { sample_count: 5, p50_ms: 2000, p95_ms: 8000, mean_ms: 3000 };
const item: BalanceModelMetric = {
  source_id: "s",
  provider: "p",
  model: "m",
  success: 3,
  failed: 1,
  cancelled: 2,
  success_rate: 0.75,
  first_text: timing,
  response_created: { ...timing, p50_ms: 50 },
  duration: { ...timing, p50_ms: 16000 },
};
it("shows distinct per-model success, first text and duration, retaining missing timings", async () => {
  const view = render(
    <Tooltip.Provider delayDuration={0}>
      <table>
        <tbody>
          <tr>
            <BalanceModelMetricCells
              value={item}
              pending={false}
              error={false}
              partial
            />
          </tr>
        </tbody>
      </table>
    </Tooltip.Provider>,
  );
  expect(screen.getByText("75.0%")).toBeVisible();
  expect(screen.getByText("3/4 · 部分")).toBeVisible();
  expect(screen.getByText("2.00 s")).toHaveClass("latency-badge", "fast");
  expect(screen.getByText("16.00 s")).toBeVisible();
  await userEvent.setup().hover(screen.getByText("2.00 s"));
  expect(await screen.findByRole("tooltip")).toHaveTextContent("P95 8.00 s");
  expect(screen.getByRole("tooltip")).toHaveTextContent("响应创建 P50 50 ms");
  view.unmount();
  render(
    <Tooltip.Provider>
      <table>
        <tbody>
          <tr>
            <BalanceModelMetricCells
              pending={false}
              error={false}
              partial={false}
            />
          </tr>
        </tbody>
      </table>
    </Tooltip.Provider>,
  );
  expect(screen.getAllByText("—")).toHaveLength(3);
  expect(screen.getByText("暂无请求")).toBeVisible();
});

it("batches all models, scopes source/key/time/model and never reuses another scope's values", async () => {
  const calls: URL[] = [];
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input);
      calls.push(url);
      if (url.searchParams.get("model") === "other")
        return new Promise<Response>((r) => {
          finish = r;
        });
      return Response.json({ data: [item] });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Harness({
    model = "",
    keyId = "s::key",
    range = "24h",
    refresh = 0,
  }) {
    const query = useBalanceModelMetrics(
      { base: location.origin, key: "", session: "test", account: true },
      {
        range,
        model,
        keyId,
        to: 1790734511,
        refresh,
        auto: false,
        enabled: true,
      },
    );
    return (
      <p>
        {query.byModel.get(balanceModelMetricID("s", "p", "m"))?.success ??
          "pending"}
      </p>
    );
  }
  const ui = (props: {
    model?: string;
    keyId?: string;
    range?: string;
    refresh?: number;
  }) => (
    <QueryClientProvider client={client}>
      <Harness {...props} />
    </QueryClientProvider>
  );
  const view = render(ui({}));
  await screen.findByText("3");
  expect(calls).toHaveLength(1);
  expect(Object.fromEntries(calls[0].searchParams)).toMatchObject({
    source_id: "s",
    key_id: "key",
    range: "24h",
    model: "",
    to: "1790734511",
  });
  view.rerender(ui({ model: "other", range: "1h", keyId: "s::new-key" }));
  expect(screen.getByText("pending")).toBeVisible();
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(Object.fromEntries(calls[1].searchParams)).toMatchObject({
    source_id: "s",
    key_id: "new-key",
    range: "1h",
    model: "other",
  });
  finish(Response.json({ data: [] }));
  await waitFor(() => expect(client.isFetching()).toBe(0));
  expect(screen.queryByText("3")).not.toBeInTheDocument();
  view.rerender(ui({ refresh: 1 }));
  await screen.findByText("3");
  expect(calls).toHaveLength(3);
});
