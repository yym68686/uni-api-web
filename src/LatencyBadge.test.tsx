import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it } from "vitest";
import { ResponseLatency } from "./LatencyBadge";

it("shows response creation by default and the later first text in the tooltip", async () => {
  render(
    <Tooltip.Provider delayDuration={0}>
      <ResponseLatency created={125} text={6200} />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("125 ms")).toBeVisible();
  expect(screen.queryByText(/6\.20 s/)).not.toBeInTheDocument();
  await userEvent.setup().tab();
  expect(await screen.findByRole("tooltip")).toHaveTextContent(
    "首个 response.output_text.delta：6.20 s",
  );
});

it("does not relabel legacy text-only latency as response creation", async () => {
  render(
    <Tooltip.Provider delayDuration={0}>
      <ResponseLatency text={6200} />
    </Tooltip.Provider>,
  );
  expect(screen.getByText("—")).toBeVisible();
  await userEvent.setup().tab();
  expect(await screen.findByRole("tooltip")).toHaveTextContent(
    "首个 response.created：—",
  );
  expect(screen.getByRole("tooltip")).toHaveTextContent(
    "首个 response.output_text.delta：6.20 s",
  );
});
