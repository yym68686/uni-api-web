import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it } from "vitest";
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
