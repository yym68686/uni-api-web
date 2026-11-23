import { fireEvent, render, screen, within } from "@testing-library/react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it } from "vitest";
import { ChannelFirstResponse, ChannelMetricCells, ChannelMetricHeaders, TotalTiming } from "./ChannelMetrics";
import { emptyStats } from "./analytics";
import type { Channel, Distribution } from "./types";

const duration: Distribution = { sample_count: 4, mean_ms: 4500, p50_ms: 3000, p95_ms: 10000, last_ms: 9000 };
const failedCreated: Distribution = { sample_count: 495, mean_ms: 5000, p50_ms: 5000, p95_ms: 30000, last_ms: 1880 };
const noCreated: Distribution = { sample_count: 0, mean_ms: null, p50_ms: null, p95_ms: null, last_ms: null };

it("hides the failed-only p50/p95 from the cell but keeps it in the tooltip", async () => {
  const row = { provider: "okai", stats: { ...emptyStats(), failed: 533, response_created: failedCreated,
    successful_response_created: noCreated, failed_response_created: failedCreated } } as Channel;
  render(<Tooltip.Provider delayDuration={0}><table><tbody><tr>
    <ChannelMetricCells row={row} actualRange={{supported:false}} columns={["latency"]} />
  </tr></tbody></table></Tooltip.Provider>);
  expect(screen.getByRole("cell")).toHaveTextContent("无成功样本");
  expect(screen.getByRole("cell")).not.toHaveTextContent("5.00 s");
  fireEvent.focus(screen.getByText("无成功样本").closest("[tabindex]")!);
  const tip = await screen.findByRole("tooltip");
  expect(tip).toHaveTextContent("失败尝试（仅供排障） · 495 次首字样本");
  expect(tip).toHaveTextContent("p50 5.00 s · p95 30.00 s");
});

it("renders the successful subset, and distinguishes missing clocks and an old endpoint", () => {
  const stats = { ...emptyStats(), success: 1, failed: 533, response_created: failedCreated,
    failed_response_created: failedCreated, first_output: duration, first_text: duration };
  const display = (successful_response_created?: Distribution) =>
    <Tooltip.Provider><ChannelFirstResponse stats={{ ...stats, successful_response_created }} showP95 /></Tooltip.Provider>;
  const view = render(display({ sample_count: 1, mean_ms: 0, p50_ms: 0, p95_ms: 0, last_ms: 0 }));
  expect(screen.getAllByText("0 ms")).toHaveLength(2);
  expect(screen.getByText("1 次样本")).toBeVisible();
  expect(screen.queryByText("5.00 s")).not.toBeInTheDocument();
  view.rerender(display(noCreated));
  expect(screen.getByText("无首字样本")).toBeVisible();
  expect(document.querySelector(".latency-badge")).toBeNull();
  view.rerender(display());
  expect(screen.getByText("未记录")).toBeVisible();
});

it("shows real body timing and its source for successful native streams without creation events", async () => {
  const stats = { ...emptyStats(), success: 4, failed: 20,
    successful_response_created: noCreated, successful_first_text: duration,
    first_text: failedCreated, first_output: failedCreated };
  render(<Tooltip.Provider delayDuration={0}><ChannelFirstResponse stats={stats} showP95 /></Tooltip.Provider>);
  expect(screen.getByText("3.00 s")).toBeVisible();
  expect(screen.getByText("10.00 s")).toBeVisible();
  expect(screen.getByText("4 次样本 · 正文")).toBeVisible();
  expect(screen.queryByText("无首字样本")).not.toBeInTheDocument();
  fireEvent.focus(screen.getByText("4 次样本 · 正文").closest("[tabindex]")!);
  const tip = await screen.findByRole("tooltip");
  expect(tip).toHaveTextContent("渠道请求发起 → 首个上游正文片段");
  expect(tip).toHaveTextContent("仅统计成功完成的渠道尝试 · 4 次首字样本");
});

it("only colors first-response p50, not p95 or dispatch waiting", () => {
  const row = { provider: "channel", stats: { ...emptyStats(), success: 4,
    successful_response_created: { ...duration, p50_ms: 5001 }, request_to_dispatch: duration } } as Channel;
  render(<Tooltip.Provider><table><tbody><tr>
    <ChannelMetricCells row={row} actualRange={{supported:false}} columns={["latency", "wait"]} />
  </tr></tbody></table></Tooltip.Provider>);
  const [latency, wait] = screen.getAllByRole("cell");
  expect(latency.querySelectorAll(".latency-badge")).toHaveLength(1);
  expect(latency.querySelector(".latency-badge")).toHaveClass("medium");
  expect(wait.querySelector(".latency-badge")).toBeNull();
});

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
  expect(screen.getByRole("columnheader", {name:"站点余额"})).toBeVisible();
});

it.each([[.98, "healthy"], [.87, "warning"], [.55, "critical"], [0, "critical"]] as const)(
  "draws a proportional, severity-coded attempt rate for %s",
  (rate, expected) => {
    const row = {provider: "channel", eligible: true, stats: {...emptyStats(), success_rate: rate, success_rate_denominator: 100}} as Channel;
    const {container} = render(<Tooltip.Provider><table><tbody><tr>
      <ChannelMetricCells row={row} actualRange={{supported:false}} columns={["success"]} />
    </tr></tbody></table></Tooltip.Provider>);
    const track = container.querySelector(".rate-track");
    expect(track).toHaveAttribute("data-tone", expected);
    expect(track?.firstElementChild).toHaveStyle({width: `${rate * 100}%`});
  },
);

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
