import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { BalanceValue } from "./ChannelMetrics";
it("does not invent a balance when an account is unlinked or its balance is missing", () => {
  const { rerender } = render(<Tooltip.Provider><BalanceValue /></Tooltip.Provider>);
  expect(screen.getByText("站点账号未核验")).toBeVisible();
  rerender(<Tooltip.Provider><BalanceValue balance={[{ account_id: "site", status: "missing", amount: null }]} /></Tooltip.Provider>);
  expect(screen.getByText("暂无站点余额")).toBeVisible();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});
it("keeps wallet colors and marks a cached amount when refresh fails", () => {
  render(<Tooltip.Provider>
    <BalanceValue detail balance={[
      ...[-1, 0, 50, 100].map(amount => ({ account_id: String(amount), status: "ok", amount })),
      { account_id: "old", status: "error", amount: 12 },
    ]} />
  </Tooltip.Provider>);
  expect(screen.getByText("$-1.00")).toHaveClass("negative");
  for (const value of ["$0.00", "$50.00"])
    expect(screen.getByText(value)).toHaveClass("balance-warning");
  expect(screen.getByText("$100.00")).toHaveClass("balance-positive");
  expect(screen.getByText("$12.00")).toBeVisible();
  expect(screen.getByText("更新失败 · 上次余额")).toBeVisible();
});
