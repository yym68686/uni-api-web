import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { ChannelDetailTrends } from "./ChannelDetailTrends";
import type { ChannelTimeseriesProps } from "./channelTimeseries";
import type { Channel } from "./types";
const props: ChannelTimeseriesProps = {
  row: {
    source_id: "do",
    provider: "channel",
    model: "alias",
    upstream_model: "upstream",
  } as Channel,
  connection: { base: "", key: "", session: "fixture", account: true },
  keyId: "do::caller",
  window: "12h",
  endpoint: "/v1/responses",
  stream: "true",
  refresh: 0,
};
const point = (timestamp: number) => ({
  timestamp,
  bucket_end: timestamp + 60,
  success: 3,
  failed: 1,
  response_created: { sample_count: 4, p50_ms: 100, p95_ms: 800 },
  first_text: { sample_count: 4, p50_ms: 250 },
  cache_rate: 0.5,
});
function mount() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ChannelDetailTrends {...props} />
    </QueryClientProvider>,
  );
}
it("switches all three trends using a single scoped request and retains the entire selected range", async () => {
  const fetch = vi.fn(async () =>
    Response.json({
      total: { cache_rate: 0.5 },
      from: 120,
      to: 120 + 180 * 60,
      bucket_seconds: 60,
      data: [
        {
          ...props.row,
          points: Array.from({ length: 180 }, (_, i) => point(120 + i * 60)),
        },
      ],
    }),
  );
  vi.stubGlobal("fetch", fetch);
  mount();
  const latency = within(
    await screen.findByRole("region", { name: "首字延迟趋势" }),
  );
  await waitFor(() => expect(latency.getAllByRole("img")).toHaveLength(180));
  expect(latency.getAllByRole("img")[0]).toHaveAttribute(
    "aria-label",
    expect.stringContaining("p50 100 ms / p95 800 ms"),
  );
  const chart = latency.getByRole("group", { name: /使用左右方向键/ });
  expect(chart).toHaveAttribute("tabindex", "0");
  expect(chart.querySelectorAll('[tabindex="0"]')).toHaveLength(0);
  const currentPoint = latency.getByText(/正文首字 p50 250 ms$/, {
    selector: ".detail-curve-value small",
  }).parentElement!;
  const lastPoint = currentPoint.textContent;
  fireEvent.keyDown(chart, { key: "Home" });
  expect(currentPoint.textContent).not.toBe(lastPoint);
  expect(currentPoint).toHaveTextContent("正文首字 p50 250 ms");
  const trends = within(screen.getByRole("region", { name: "渠道趋势曲线" }));
  expect(
    trends.getByRole("button", { name: "图表", pressed: true }),
  ).toBeVisible();
  fireEvent.click(
    trends.getByRole("button", { name: "数据表", pressed: false }),
  );
  expect(
    trends.getByRole("button", { name: "数据表", pressed: true }),
  ).toBeVisible();
  expect(
    trends.queryByRole("region", { name: "首字延迟趋势" }),
  ).not.toBeInTheDocument();
  const table = trends.getByRole("table", { name: "渠道分时数据" });
  expect(within(table).getAllByRole("row")).toHaveLength(181);
  expect(within(table).getAllByRole("row")[1]).toHaveTextContent("p50 100 ms");
  expect(within(table).getAllByRole("row")[1]).toHaveTextContent("75.0%");
  expect(within(table).getAllByRole("row")[1]).toHaveTextContent("50.0%");
  fireEvent.click(trends.getByRole("button", { name: "图表" }));
  expect(trends.queryByRole("table")).not.toBeInTheDocument();
  expect(currentPoint.textContent).not.toBe(lastPoint);
  expect(
    within(screen.getByRole("region", { name: "首字延迟趋势" })).getAllByRole(
      "img",
    ),
  ).toHaveLength(180);
  expect(fetch).toHaveBeenCalledTimes(1);
  const url = new URL(
    String((fetch.mock.calls as unknown as [string][])[0][0]),
    location.origin,
  );
  expect(Object.fromEntries(url.searchParams)).toMatchObject({
    source_id: "do",
    key_id: "caller",
    provider: "channel",
    model: "alias",
    upstream_model: "upstream",
    range: "12h",
    endpoint: "/v1/responses",
    stream: "true",
    timeseries: "true",
  });
});
it("keeps missing samples and gaps empty while plotting real zero success", async () => {
  const points = [
    point(120),
    {
      ...point(180),
      success: 0,
      failed: 0,
      cache_rate: null,
      response_created: { sample_count: 0, p50_ms: null, p95_ms: null },
    },
    { ...point(240), success: 0, failed: 2, covered: false, cache_rate: 0 },
    point(420),
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({
        total: {},
        from: 120,
        to: 480,
        bucket_seconds: 60,
        data: [{ ...props.row, points }],
      }),
    ),
  );
  mount();
  const latency = within(
      await screen.findByRole("region", { name: "首字延迟趋势" }),
    ),
    success = within(screen.getByRole("region", { name: "成功率趋势" }));
  await waitFor(() => expect(latency.getAllByRole("img")).toHaveLength(3));
  expect(success.getAllByRole("img")).toHaveLength(3);
  expect(success.getAllByRole("img")[1]).toHaveAttribute(
    "aria-label",
    expect.stringContaining("成功率 0.0%"),
  );
  for (const path of document.querySelectorAll(".detail-curve-line"))
    expect(path.getAttribute("d")?.match(/M/g)).toHaveLength(3);
  fireEvent.click(screen.getByRole("button", { name: "数据表" }));
  const rows = within(
    screen.getByRole("table", { name: "渠道分时数据" }),
  ).getAllByRole("row");
  expect(within(rows[2]).getAllByText("未记录")).toHaveLength(3);
  expect(within(rows[3]).getAllByText("0.0%")).toHaveLength(2);
  expect(rows[3]).toHaveTextContent("覆盖不完整");
});
it("does not fabricate zero measurements for empty history", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => Response.json({ total: {}, from: 1, to: 100, data: [] })),
  );
  mount();
  expect(await screen.findByText("当前范围暂无首字延迟样本。")).toBeVisible();
  expect(screen.getByText("当前范围暂无成功率样本。")).toBeVisible();
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("group", { name: "趋势展示方式" }),
  ).not.toBeInTheDocument();
});
