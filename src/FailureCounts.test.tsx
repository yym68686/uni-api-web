import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { FailureCounts } from "./FailureCounts";
import type { Channel } from "./types";

it("shows scoped reason counts once and keeps missing historical reasons separate", () => {
  render(
    <FailureCounts
      channels={
        [
          {
            stats: {
              failed: 8,
              failure_reasons: {
                responses_max_output_tokens: 2,
                missing_response_completed: 3,
                upstream_http_429: 1,
              },
            },
          },
          {
            stats: {
              failed: 2,
              failure_reasons: { responses_max_output_tokens: 2 },
            },
          },
        ] as unknown as Channel[]
      }
    />,
  );
  const region = within(screen.getByRole("region", { name: "失败原因次数" }));
  for (const [label, value] of [
    ["输出达到上限", "4 次"],
    ["缺少完成事件", "3 次"],
    ["上游 HTTP 429", "1 次"],
    ["历史失败／原因未记录", "2 次"],
  ]) {
    expect(
      within(region.getByText(label).parentElement!).getByText(value),
    ).toBeVisible();
  }
  expect(region.getByText("10 次")).toBeVisible();
});

it("shows the empty result without explanatory prose", () => {
  render(<FailureCounts channels={[]} />);
  expect(screen.getByText("当前范围暂无失败记录")).toBeVisible();
  expect(screen.queryByText(/首字延迟沿用列表口径/)).not.toBeInTheDocument();
});
