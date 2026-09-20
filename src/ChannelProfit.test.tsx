import { expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { channelProfit, channelProfitCost, ChannelProfit } from "./ChannelProfit";
import { ChannelMetricCells, ChannelMetricHeaders } from "./ChannelMetrics";
import { emptyStats } from "./analytics";
import type { Channel } from "./types";
import type { SubChannelSpendResult } from "./SubChannelSpend";

it("uses the requested numeric formula and revenue-based profit margin without converting actual spend", () => {
  expect(channelProfit(100, 5)).toEqual({
    amount: 12.25,
    margin: 12.25 / 17.25,
  });
  expect(channelProfit(100, 20)).toEqual({
    amount: -2.75,
    margin: -2.75 / 17.25,
  });
  expect(channelProfit(0, 5)).toEqual({ amount: -5, margin: null });
  expect(channelProfit(0, 0)).toEqual({ amount: 0, margin: null });
  expect(channelProfit(100, 0)).toEqual({ amount: 17.25, margin: 1 });
  expect(channelProfit(100, null)).toBeNull();
  expect(channelProfit(undefined, 1)).toBeNull();
  expect(channelProfit(Number.NaN, 1)).toBeNull();
});

it("displays yuan on the first line and profit margin below, including losses and zero revenue", () => {
  const app = render(
    <Tooltip.Provider>
      <ChannelProfit estimated={100} actual={5} />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("¥12.25")).toBeVisible();
  expect(screen.getByLabelText("利润率 71.01%")).toBeVisible();
  expect(screen.getByText("¥12.25").parentElement).toHaveClass(
    "channel-profit",
    "profit-positive",
  );
  app.rerender(
    <Tooltip.Provider>
      <ChannelProfit estimated={100} actual={20} />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("¥-2.75").parentElement).toHaveClass("negative");
  expect(screen.getByLabelText("利润率 -15.94%")).toBeVisible();
  app.rerender(
    <Tooltip.Provider>
      <ChannelProfit estimated={0} actual={5} />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("¥-5.00")).toBeVisible();
  expect(screen.getByLabelText("利润率 —")).toBeVisible();
});

it("uses the displayed channel spending source and never falls back to a whole-day total for pending imported bills", () => {
  const row = {
    provider: "sub2api-test",
    eligible: true,
    stats: { ...emptyStats(), estimated_cost_usd: 100 },
  } as Channel;
  const balance = {
    data: { provider: row.provider, status: "complete", actual_cost_usd: 20 },
  };
  const receipt: SubChannelSpendResult = {
    isPending: false,
    isError: false,
    data: {
      status: "complete",
      actual_cost_usd: 5,
      requests: 1,
      from: 1,
      to: 100,
      checked_at: 100,
      key_id: 42,
      scope: "matched_requests",
    },
  };
  const ui = (
    imported: boolean,
    supported: boolean,
    spend?: SubChannelSpendResult,
  ) => (
    <Tooltip.Provider>
      <table>
        <thead>
          <tr>
            <ChannelMetricHeaders />
          </tr>
        </thead>
        <tbody>
          <tr>
            <ChannelMetricCells
              row={row}
              balance={balance}
              actualRange={{ supported }}
              importedChannel={imported}
              spend={spend}
            />
          </tr>
        </tbody>
      </table>
    </Tooltip.Provider>
  );
  const app = render(ui(true, false, receipt));
  const profitIndex = screen
    .getAllByRole("columnheader")
    .findIndex((header) => header.textContent === "利润 ");
  expect(profitIndex).toBeGreaterThan(0);
  const profitCell = () =>
    within(screen.getAllByRole("row")[1]).getAllByRole("cell")[profitIndex];
  expect(profitCell()).toHaveTextContent("¥12.25");
  app.rerender(
    ui(true, true, {
      ...receipt,
      data: { ...receipt.data!, status: "pending", actual_cost_usd: null },
    }),
  );
  expect(profitCell()).not.toHaveTextContent("¥");
  app.rerender(
    ui(true, true, {
      ...receipt,
      data: {
        ...receipt.data!,
        status: "unmatched",
        actual_cost_usd: null,
        matched_cost_usd: 5,
        total_attempts: 10,
        matched_attempts: 9,
        missing_identifiers: 1,
      },
    }),
  );
  expect(screen.getByText("≥$5.00")).toBeVisible();
  expect(profitCell()).toHaveTextContent("≤¥12.25上限≤71.01%");
  app.rerender(ui(true, true, receipt));
  expect(profitCell()).toHaveTextContent("¥12.25");
  expect(profitCell()).not.toHaveTextContent("≤");
  expect(profitCell()).not.toHaveTextContent("上限");
  app.rerender(ui(true, true));
  expect(profitCell()).not.toHaveTextContent("¥");
  app.rerender(ui(false, false));
  expect(profitCell()).not.toHaveTextContent("¥");
  app.rerender(ui(false, true));
  expect(profitCell()).not.toHaveTextContent("¥");
  app.rerender(
    ui(true, true, {
      ...receipt,
      data: { ...receipt.data!, scope: "sub2api_business_key" },
    }),
  );
  expect(profitCell()).not.toHaveTextContent("¥");
});

it("shows the screenshot profit upper bound and explains its uncertainty", async () => {
  render(<Tooltip.Provider><ChannelProfit estimated={105.6885} actual={7.6924857} upperBound /></Tooltip.Provider>);
  const amount = screen.getByText("≤¥10.538781");
  expect(amount).toBeVisible();
  expect(screen.getByText("上限")).toBeVisible();
  expect(screen.getByLabelText("利润率上限 ≤57.81%")).toBeVisible();
  expect(amount.closest(".channel-profit")).toHaveClass("profit-upper-bound");
  expect(amount.closest(".channel-profit")).not.toHaveClass("profit-positive");
  fireEvent.focus(amount.closest(".tip-target")!);
  expect(await screen.findByRole("tooltip")).toHaveTextContent("实际利润及利润率可能更低");
});

it("keeps negative upper bounds and zero-revenue bounds explicit", () => {
  const view = render(<Tooltip.Provider><ChannelProfit estimated={100} actual={20} upperBound /></Tooltip.Provider>);
  expect(screen.getByText("≤¥-2.75").closest(".channel-profit")).toHaveClass("negative");
  expect(screen.getByLabelText("利润率上限 ≤-15.94%")).toBeVisible();
  view.rerender(<Tooltip.Provider><ChannelProfit estimated={100} actual={0} salePercent={0} upperBound /></Tooltip.Provider>);
  expect(screen.getByText("≤¥0.00")).toBeVisible();
  expect(screen.getByLabelText("利润率上限 —")).toBeVisible();
});

it("uses only reconciled per-request costs for an upper bound, never missing or shared totals", () => {
  const spend: NonNullable<SubChannelSpendResult["data"]> = {
    status: "unmatched", scope: "matched_requests", actual_cost_usd: null,
    from: 1, to: 2, matched_cost_usd: 5, matched_attempts: 9, total_attempts: 10,
  };
  expect(channelProfitCost(spend)).toEqual({ actual: 5, upperBound: true });
  expect(channelProfitCost({ ...spend, status: "error", sync_error: true })).toEqual({ actual: 5, upperBound: true });
  expect(channelProfitCost({ ...spend, matched_cost_usd: 0, matched_attempts: 0, confirmed_unbilled_attempts: 1 })).toEqual({ actual: 0, upperBound: true });
  expect(channelProfitCost({ ...spend, matched_cost_usd: 0, matched_attempts: 0 })).toBeUndefined();
  expect(channelProfitCost({ ...spend, scope: "sub2api_business_key" })).toBeUndefined();
  for (const value of [undefined, Number.NaN, Infinity, -1])
    expect(channelProfitCost({ ...spend, matched_cost_usd: value })).toBeUndefined();
  expect(channelProfitCost({ ...spend, status: "complete", actual_cost_usd: null })).toBeUndefined();
});

it("uses each model's sale percentage, including explicit free pricing", () => {
  expect(channelProfit(100, 5, 15)).toEqual({
    amount: 98.5,
    margin: 98.5 / 103.5,
  });
  expect(channelProfit(100, 5, 0)).toEqual({ amount: -5, margin: null });
  expect(channelProfit(100, 5, -1)).toBeNull();
  expect(channelProfit(100, 5, Number.NaN)).toBeNull();
  const row = {
    provider: "p",
    model: "gemini-3.1-pro-search",
    eligible: true,
    stats: { ...emptyStats(), estimated_cost_usd: 100 },
  } as Channel;
  const ui = () => (
    <Tooltip.Provider>
      <table>
        <tbody>
          <tr>
            <ChannelMetricCells
              row={row}
              spend={{
                isPending: false,
                isError: false,
                data: {
                  status: "complete",
                  actual_cost_usd: 5,
                  scope: "matched_requests",
                  from: 1,
                  to: 2,
                  requests: 1,
                  checked_at: 2,
                  key_id: 42,
                },
              }}
              actualRange={{ supported: true }}
              balance={{
                data: { provider: "p", status: "complete", actual_cost_usd: 5 },
              }}
            />
          </tr>
        </tbody>
      </table>
    </Tooltip.Provider>
  );
  const view = render(ui());
  expect(screen.getByText("¥98.50")).toBeVisible();
  row.stats.sale_percent = 10;
  view.rerender(ui());
  expect(screen.getByText("¥64.00")).toBeVisible();
  row.stats.sale_percent = 0;
  view.rerender(ui());
  expect(screen.getByText("¥-5.00")).toBeVisible();
  expect(screen.getByLabelText("利润率 —")).toBeVisible();
});
