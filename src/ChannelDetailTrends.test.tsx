import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { ChannelDetailTrends } from "./ChannelDetailTrends";
import { CacheTrend } from "./CacheTrend";
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
      <CacheTrend {...props} />
    </QueryClientProvider>,
  );
}
it("shares one scoped request with cache history and retains the entire selected range", async () => {
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
  expect(latency.getAllByRole("img")[0]).toHaveAttribute(
    "aria-label",
    expect.stringContaining("正文首字 p50 250 ms"),
  );
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
      response_created: { sample_count: 0, p50_ms: null, p95_ms: null },
    },
    { ...point(240), success: 0, failed: 2 },
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
});
