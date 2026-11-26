// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { emptyStats } from "./analytics";
import { rowId } from "./format";
import { OverviewProfitTable } from "./OverviewProfitTable";
import { summarizeOverviewProfit } from "./overviewProfit";
import type { SubChannelSpend, SubChannelSpendResult } from "./SubChannelSpend";
import type { Channel } from "./types";

afterEach(cleanup);

function row(provider: string, source_id = "a", model = "gpt-6", stats = {}): Channel {
  return {
    provider, source_id, source_name: `来源 ${source_id}`, model, upstream_model: model,
    endpoint: "all", stream: null, engine: "gpt", eligible: true, reason: "eligible",
    stats: { ...emptyStats(), started: 1, requests: 1, usage_samples: 1, priced_samples: 1,
      estimated_cost_usd: 100, sale_percent: 10, ...stats },
  };
}
function bill(actual: number | null, data: Partial<SubChannelSpend> = {}): SubChannelSpendResult {
  return { isError: false, isPending: false, data: {
    status: "complete", scope: "matched_requests", from: 100, to: 200, actual_cost_usd: actual, ...data,
  } };
}

it("nets the total by channel but ranks the top eight channel and model pairs on each side", () => {
  const gains = Array.from({ length: 10 }, (_, i) => row(`gain-${i}`));
  const losses = Array.from({ length: 10 }, (_, i) => row(`loss-${i}`));
  const anotherModel = row("gain-0", "a", "claude-opus", { sale_percent: 15 });
  const anotherSource = row("gain-0", "b");
  const spend = new Map([
    ...gains.map((entry, i) => [rowId(entry), bill(68 - i)] as const),
    ...losses.map((entry, i) => [rowId(entry), bill(70 + i)] as const),
    [rowId(anotherModel), bill(10.5)],
    [rowId(anotherSource), bill(99)],
  ]);
  const rows = [...gains, ...losses, anotherModel, anotherSource, gains[0]];
  const profit = summarizeOverviewProfit(rows, spend, false);
  expect(profit).toMatchObject({ amount: 63, counted: 21, omitted: 0, provisional: false });
  expect(profit.gains.map(entry => entry.row.provider)).toEqual(["gain-0","gain-9","gain-8","gain-7","gain-6","gain-5","gain-4","gain-3"]);
  expect(profit.losses.map(entry => entry.row.provider)).toEqual(["gain-0","loss-9","loss-8","loss-7","loss-6","loss-5","loss-4","loss-3"]);
  expect(profit.gains[0].row).toBe(anotherModel);
  expect(profit.losses[0].row).toBe(anotherSource);
  expect(profit.gains[0].id).not.toEqual(profit.losses[0].id);
});

it("ranks sibling models separately, combining only historical mappings of the same model in the same source", () => {
  const original = row("same", "a", "public-a");
  const alias = { ...original, upstream_model: "older-upstream" };
  const sibling = row("same", "a", "public-b");
  const otherSource = row("same", "b", "public-a");
  const rows = [original, alias, sibling, otherSource, alias];
  const spend = new Map([
    [rowId(original), bill(75)], [rowId(alias), bill(73)],
    [rowId(sibling), bill(89)], [rowId(otherSource), bill(72)],
  ]);
  const profit = summarizeOverviewProfit(rows, spend, false);
  expect(profit).toMatchObject({ amount: -33, counted: 2, omitted: 0, gains: [] });
  expect(profit.losses).toMatchObject([
    { row: sibling, amount: -20 }, { row: original, amount: -10 }, { row: otherSource, amount: -3 },
  ]);
});

it("ranks reconciled models, omits uncertain channels from the total and keeps proven no-revenue losses", () => {
  const history = row("history");
  const unknown = row("unknown");
  const unknownSibling = row("unknown", "a", "gpt-7");
  const unpriced = row("unpriced", "a", "gpt-8", { usage_samples: 2 });
  const failedAttempt = row("actual-only", "a", "gpt-6", {
    requests: 0, usage_samples: 0, priced_samples: 0, estimated_cost_usd: null,
  });
  const account = row("business-key-total");
  const noActivity = row("unused", "a", "gpt-6", {
    started: 0, requests: 0, usage_samples: 0, priced_samples: 0, estimated_cost_usd: null,
  });
  const spend = new Map([
    [rowId(history), bill(null, { status: "history_limited", matched_attempts: 1, observed_cost_usd: 39 })],
    [rowId(unknown), bill(5)],
    [rowId(unknownSibling), bill(null, { status: "pending", matched_attempts: 1, matched_cost_usd: 10 })],
    [rowId(unpriced), bill(1)],
    [rowId(failedAttempt), bill(7)],
    [rowId(account), bill(60, { scope: "sub2api_business_key" })],
  ]);
  const total = summarizeOverviewProfit([history, unknown, unknownSibling, unpriced, failedAttempt, account, noActivity], spend, false);
  expect(total).toMatchObject({ amount: 23, counted: 2, omitted: 3, provisional: true, pending: true });
  expect(total.gains).toMatchObject([{ row: unknown, amount: 64 }, { row: history, amount: 30, provisional: true }]);
  expect(total.losses).toMatchObject([{ row: failedAttempt, amount: -7 }]);
  expect(summarizeOverviewProfit([unknown], new Map([[rowId(unknown), { data: undefined, isPending: false, isError: true }]]), false))
    .toMatchObject({ amount: null, failed: true, omitted: 1 });
  expect(summarizeOverviewProfit([], new Map(), true).amount).toBeNull();
  expect(summarizeOverviewProfit([], new Map(), false).amount).toBe(0);
});

it("shows the exact model and source on each link and marks provisional results", () => {
  const current = row("current"), removed = row("removed", "b");
  const spend = new Map([
    [rowId(current), bill(59)],
    [rowId(removed), bill(null, { status: "history_limited", matched_attempts: 1, observed_cost_usd: 49 })],
  ]);
  const { gains } = summarizeOverviewProfit([current, removed], spend, false);
  const onChannel = vi.fn();
  render(<OverviewProfitTable title="盈利渠道 TOP 8" entries={gains} empty="无数据"
    showSource onChannel={onChannel} />);
  const table = within(screen.getByRole("region", { name: "盈利渠道 TOP 8" }));
  expect(table.getByText("removed")).toBeTruthy();
  expect(table.getByText("gpt-6 · 来源 b")).toBeTruthy();
  expect(table.getByText("暂计")).toBeTruthy();
  table.getByRole("button", { name: "在渠道观测筛选 current · gpt-6 · 来源 a" }).click();
  expect(onChannel).toHaveBeenCalledWith(current);
  table.getByRole("button", { name: "在渠道观测筛选 removed · gpt-6 · 来源 b" }).click();
  expect(onChannel).toHaveBeenLastCalledWith(removed);
});
