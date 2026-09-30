import { expect, it, vi } from "vitest";
import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChannelSortDialog } from "./ChannelSortDialog";
import { qualityFirstRules } from "./channelSorting";
import type { SortTemplate } from "./channelSortPreferences";

it("saves, selects, renames and deletes templates without changing applied rules until Apply", async () => {
  const apply = vi.fn();
  const user = userEvent.setup();
  function Harness() {
    const [templates, setTemplates] = useState<SortTemplate[]>([]);
    return (
      <ChannelSortDialog
        rules={qualityFirstRules}
        onApply={apply}
        templates={templates}
        onSaveTemplate={(name, rules) => {
          setTemplates((old) => [...old, { id: "t", name, rules }]);
          return "t";
        }}
        onRenameTemplate={(id, name) =>
          setTemplates((old) =>
            old.map((t) => (t.id === id ? { ...t, name } : t)),
          )
        }
        onDeleteTemplate={(id) =>
          setTemplates((old) => old.filter((t) => t.id !== id))
        }
      />
    );
  }
  render(<Harness />);
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  await user.type(screen.getByLabelText("排序模板名称"), "质量方案");
  await user.click(screen.getByRole("button", { name: "保存为模板" }));
  expect(screen.getByLabelText("排序模板")).toHaveValue("t");
  expect(screen.getByRole("button", { name: "保存为模板" })).toBeDisabled();
  expect(apply).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "应用排序" }));
  expect(apply).toHaveBeenCalledWith(qualityFirstRules, "t");
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  await user.selectOptions(screen.getByLabelText("排序模板"), "t");
  await user.clear(screen.getByLabelText("排序模板名称"));
  await user.type(screen.getByLabelText("排序模板名称"), "高级模型");
  await user.click(screen.getByRole("button", { name: "重命名模板" }));
  expect(screen.getByRole("option", { name: "高级模型" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "删除模板" }));
  expect(
    screen.queryByRole("option", { name: "高级模型" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("第 1 排序依据")).toHaveValue("quality");
  expect(apply).toHaveBeenCalledTimes(1);
});

it("prefills the model's previous template for a new scope without auto-applying it", async () => {
  const apply = vi.fn();
  const user = userEvent.setup();
  render(
    <ChannelSortDialog
      rules={[]}
      scopeLabel="gpt-6-astra · Fugue · Key 2"
      templates={[{ id: "q", name: "高级模型", rules: qualityFirstRules }]}
      suggestedTemplateId="q"
      onSaveTemplate={() => "new"}
      onApply={apply}
    />,
  );
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByRole("dialog")).toHaveTextContent(
    "gpt-6-astra · Fugue · Key 2",
  );
  expect(screen.getByLabelText("排序模板")).toHaveValue("q");
  expect(screen.getByLabelText("第 1 排序依据")).toHaveValue("quality");
  expect(apply).not.toHaveBeenCalled();
  await user.selectOptions(screen.getByLabelText("第 1 排序方向"), "desc");
  await user.click(screen.getByRole("button", { name: "应用排序" }));
  expect(apply).toHaveBeenCalledWith([
    { field: "quality", direction: "desc" },
    qualityFirstRules[1],
  ]);
});

it("allows status to precede existing criteria and explains both directions", async () => {
  const apply = vi.fn(),
    user = userEvent.setup();
  render(<ChannelSortDialog rules={qualityFirstRules} onApply={apply} />);
  await user.click(screen.getByRole("button", { name: "多条件排序" }));
  await user.click(screen.getByRole("button", { name: "添加排序依据" }));
  await user.selectOptions(screen.getByLabelText("第 3 排序依据"), "status");
  expect(
    screen.getByRole("option", { name: "可用/冷却优先（升序）" }),
  ).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("第 3 排序方向"), "desc");
  expect(
    screen.getByRole("option", { name: "临时停用优先（降序）" }),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "上移第 3 项" }));
  await user.click(screen.getByRole("button", { name: "上移第 2 项" }));
  await user.click(screen.getByRole("button", { name: "应用排序" }));
  expect(apply).toHaveBeenCalledWith([
    { field: "status", direction: "desc" },
    ...qualityFirstRules,
  ]);
});

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
