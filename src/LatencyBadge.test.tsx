import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it } from "vitest";
import { LatencyBadge, ResponseLatency } from "./LatencyBadge";

it("uses exact 5s and 10s badge boundaries and preserves zero", () => {
  const { container } = render(<>{[0, 5000, 5001, 10000, 10001, null].map((value, i) =>
    <LatencyBadge key={i} value={value} />
  )}</>);
  expect(container.querySelectorAll(".fast")).toHaveLength(2);
  expect(container.querySelectorAll(".medium")).toHaveLength(2);
  expect(container.querySelectorAll(".slow")).toHaveLength(1);
  expect(container).not.toHaveTextContent("NaN");
});

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
    "首个上游正文片段：6.20 s",
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
    "首个上游正文片段：6.20 s",
  );
});
