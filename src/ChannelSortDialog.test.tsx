import { expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChannelSortDialog } from "./ChannelSortDialog";
import { qualityFirstRules } from "./channelSorting";

it("edits priorities and directions in a cancelable draft, with reset and reusable presets", async () => {
  const apply = vi.fn();
  const user = userEvent.setup();
  render(<ChannelSortDialog rules={qualityFirstRules} onApply={apply} />);
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  const dialog = screen.getByRole("dialog", { name: "渠道排序" });
  expect(within(dialog).getByLabelText("第 1 排序依据")).toHaveValue("quality");
  await user.click(within(dialog).getByRole("button", { name: "上移第 2 项" }));
  expect(within(dialog).getByLabelText("第 1 排序依据")).toHaveValue(
    "probability",
  );
  await user.selectOptions(
    within(dialog).getByLabelText("第 1 排序方向"),
    "asc",
  );
  await user.click(
    within(dialog).getByRole("button", { name: "添加排序依据" }),
  );
  expect(within(dialog).getByLabelText("第 3 排序依据")).toHaveValue(
    "multiplier",
  );
  expect(
    within(within(dialog).getByLabelText("第 3 排序依据")).getByRole("option", {
      name: "不降智概率",
    }),
  ).toBeDisabled();
  await user.selectOptions(
    within(dialog).getByLabelText("第 3 排序依据"),
    "cache",
  );
  await user.click(within(dialog).getByRole("button", { name: "应用排序" }));
  expect(apply).toHaveBeenLastCalledWith([
    { field: "probability", direction: "asc" },
    { field: "quality", direction: "asc" },
    { field: "cache", direction: "desc" },
  ]);
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  await user.click(screen.getByRole("button", { name: "删除第 1 项" }));
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(apply).toHaveBeenCalledTimes(1);
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByLabelText("第 1 排序依据")).toHaveValue("quality");
  await user.click(screen.getByRole("button", { name: "恢复配置顺序" }));
  expect(screen.queryByLabelText("第 1 排序依据")).not.toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: "不降智优先，再按概率" }),
  );
  expect(screen.getByLabelText("第 2 排序方向")).toHaveValue("desc");
  await user.click(screen.getByRole("button", { name: "恢复配置顺序" }));
  await user.click(screen.getByRole("button", { name: "应用排序" }));
  expect(apply).toHaveBeenLastCalledWith([]);
});
