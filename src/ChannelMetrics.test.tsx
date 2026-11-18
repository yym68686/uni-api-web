import { fireEvent, render, screen, within } from "@testing-library/react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it } from "vitest";
import { ChannelMetricCells, ChannelMetricHeaders, TotalTiming } from "./ChannelMetrics";
import { emptyStats } from "./analytics";
import type { Channel, Distribution } from "./types";

const duration: Distribution = { sample_count: 4, mean_ms: 4500, p50_ms: 3000, p95_ms: 10000, last_ms: 9000 };

it("mounts only compact observation metrics and keeps counts aligned", () => {
  const row = { provider: "channel", eligible: true, stats: { ...emptyStats(), success: 9, failed: 1, success_rate: .9, success_rate_denominator: 10 } } as Channel;
  render(<Tooltip.Provider><table>
    <thead><tr><ChannelMetricHeaders layout="run" compact /></tr></thead>
    <tbody><tr><ChannelMetricCells row={row} actualRange={{supported:false}} layout="run" compact /></tr></tbody>
  </table></Tooltip.Provider>);
  expect(screen.getAllByRole("columnheader")).toHaveLength(3);
  expect(screen.getAllByRole("cell")).toHaveLength(3);
  expect(screen.getByText("90.0%")).toBeVisible();
  expect(screen.getByText("9/10")).toBeVisible();
  expect(screen.queryByText("估算消费")).not.toBeInTheDocument();
  expect(screen.getByRole("columnheader", {name:/余额\s*\/\s*额度/})).toBeVisible();
});

it("places total-time p50/p95 immediately after first-token latency with aligned cells", () => {
  const row = { provider: "channel", eligible: true, stats: { ...emptyStats(), started: 5, duration } } as Channel;
  render(<Tooltip.Provider><table>
    <thead><tr><ChannelMetricHeaders /></tr></thead>
    <tbody><tr><ChannelMetricCells row={row} actualRange={{ supported: false }} /></tr></tbody>
  </table></Tooltip.Provider>);
  const headers = screen.getAllByRole("columnheader");
  const index = headers.findIndex(h => h.textContent?.startsWith("首字"));
  expect(headers[index + 1]).toHaveTextContent("总时间 p50 / p95");
  expect(headers[index + 2]).toHaveTextContent("Token / 缓存率");
  const cells = within(screen.getAllByRole("row")[1]).getAllByRole("cell");
  expect(cells).toHaveLength(headers.length);
  expect(within(cells[index + 1]).getByLabelText("总时间 p50 3.00 s")).toBeVisible();
  expect(within(cells[index + 1]).getByLabelText("总时间 p95 10.00 s")).toBeVisible();
});

it("explains that attempts, not end-to-end requests or only successful attempts, own the clock", async () => {
  render(<Tooltip.Provider delayDuration={0}><TotalTiming value={duration} attempts={5} /></Tooltip.Provider>);
  fireEvent.focus(screen.getByLabelText("渠道总时间"));
  const tip = await screen.findByRole("tooltip");
  expect(tip).toHaveTextContent("包含成功、失败和取消的已结束尝试");
  expect(tip).toHaveTextContent("不包含尝试此渠道之前");
  expect(tip).toHaveTextContent("直方图上界估计");
  expect(tip).toHaveTextContent("部分尝试未记录耗时");
});

it("keeps zero valid and does not fabricate missing or old-server measurements", () => {
  const app = render(<Tooltip.Provider><TotalTiming /></Tooltip.Provider>);
  expect(screen.getByLabelText("渠道总时间")).toHaveTextContent("未记录");
  app.rerender(<Tooltip.Provider><TotalTiming value={{ ...duration, sample_count: 0, p50_ms: null, p95_ms: null }} /></Tooltip.Provider>);
  expect(screen.getByLabelText("渠道总时间")).toHaveTextContent("未记录");
  app.rerender(<Tooltip.Provider><TotalTiming value={{ ...duration, p50_ms: 0, p95_ms: 0 }} /></Tooltip.Provider>);
  expect(screen.getByLabelText("总时间 p50 0 ms")).toBeVisible();
  expect(screen.getByLabelText("总时间 p95 0 ms")).toBeVisible();
});
