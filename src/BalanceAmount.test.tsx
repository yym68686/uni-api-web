import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { BalanceValue } from "./ChannelMetrics";
it("uses the same colors for channel wallets and each remaining-quota window", () => {
  render(
    <Tooltip.Provider>
      <BalanceValue
        detail
        balance={{
          provider: "site",
          status: "complete",
          keys: [
            {
              position: 1,
              status: "ok",
              kind: "wallet",
              currency: "USD",
              amount: -1,
            },
            {
              position: 2,
              status: "ok",
              windows: [
                { window: "5h", remaining: 0 },
                { window: "7d", remaining: 50 },
                { window: "30d", remaining: 100 },
              ],
            },
            { position: 3, status: "timeout", amount: -9 },
            { position: 4, status: "ok", unlimited: true, amount: -9 },
          ],
        }}
      />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("$-1.00")).toHaveClass("negative");
  for (const value of ["$0.00", "$50.00"])
    expect(screen.getByText(value)).toHaveClass("balance-warning");
  expect(screen.getByText("$100.00")).toHaveClass("balance-positive");
  expect(screen.queryByText("$-9.00")).not.toBeInTheDocument();
  expect(screen.getByText("无限额")).toBeVisible();
});
