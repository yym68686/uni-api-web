import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { parseTableColumns, TableColumns, useTableColumns } from "./TableColumns";

const columns = [{id:"rate",label:"成功率"},{id:"balance",label:"余额"},{id:"status",label:"状态"}] as const;
afterEach(() => localStorage.clear());

it("toggles and reorders visible columns, remembers the choice and resets to defaults", async () => {
  const user = userEvent.setup();
  function Fixture() {
    const [value, set] = useTableColumns("columns-test", columns);
    return <TableColumns choices={columns} value={value ?? ["rate","balance"]} onChange={set} onReset={() => set(null)} />;
  }
  let app = render(<Fixture />);
  await user.click(screen.getByText("数据列"));
  await user.click(screen.getByRole("checkbox", {name:"状态"}));
  await user.click(screen.getByRole("button", {name:"上移状态列"}));
  await user.click(screen.getByRole("checkbox", {name:"成功率"}));
  const labels = () => within(screen.getByRole("list")).getAllByRole("checkbox").map(input => input.parentElement?.textContent);
  expect(labels()).toEqual(["状态","余额","成功率"]);
  app.unmount();
  app = render(<Fixture />);
  await user.click(screen.getByText("数据列"));
  expect(labels()).toEqual(["状态","余额","成功率"]);
  expect(screen.getByRole("checkbox", {name:"成功率"})).not.toBeChecked();
  await user.click(screen.getByRole("button", {name:"恢复默认列"}));
  expect(labels()).toEqual(["成功率","余额","状态"]);
});

it("ignores obsolete and duplicate preferences", () => {
  expect(parseTableColumns(["status","unknown","status","rate"], columns)).toEqual(["status","rate"]);
});

const choices = [{id: "a", label: "路由状态"}, {id: "b", label: "当前并发"}, {id: "c", label: "余额"}, {id: "d", label: "隐藏列"}];

it.each([
  ["路由状态", "余额", "after", ["b", "c", "a"]],
  ["余额", "路由状态", "before", ["c", "a", "b"]],
] as const)("previews moving %s relative to %s before committing", (source, target, position, expected) => {
  const change = vi.fn();
  render(<TableColumns inline choices={choices} value={["a", "b", "c"]} onChange={change} onReset={vi.fn()} />);
  const row = (label: string) => screen.getByLabelText(label).closest("li")!;
  const grip = row(source).querySelector("[draggable]")!;
  const dataTransfer = { setData: vi.fn(), effectAllowed: "", dropEffect: "" };
  fireEvent.dragStart(grip, { dataTransfer });
  fireEvent.dragOver(row(target), { dataTransfer });
  expect(row(source)).toHaveAttribute("data-column-dragging");
  expect(row(target)).toHaveAttribute("data-column-drop", position);
  expect(row(target).querySelector(".table-column-drop-preview")).toHaveTextContent(source);
  expect(change).not.toHaveBeenCalled();
  fireEvent.dragOver(row("隐藏列"), { dataTransfer });
  expect(row(target)).not.toHaveAttribute("data-column-drop");
  fireEvent.dragOver(row(target), { dataTransfer });
  fireEvent.drop(row(target), { dataTransfer });
  expect(change).toHaveBeenCalledExactlyOnceWith(expected);
  expect(row(target)).not.toHaveAttribute("data-column-drop");
});

it("removes the preview when a drag is cancelled", () => {
  const change = vi.fn();
  render(<TableColumns inline choices={choices} value={["a", "b", "c"]} onChange={change} onReset={vi.fn()} />);
  const items = screen.getAllByRole("listitem");
  const grip = items[0].querySelector("[draggable]")!;
  const dataTransfer = { setData: vi.fn() };
  fireEvent.dragStart(grip, { dataTransfer });
  fireEvent.dragOver(items[1], { dataTransfer });
  fireEvent.dragEnd(grip, { dataTransfer });
  expect(items[1]).not.toHaveAttribute("data-column-drop");
  expect(change).not.toHaveBeenCalled();
});
