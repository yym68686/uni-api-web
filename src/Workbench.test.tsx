import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { WorkbenchDisclosure } from "./Workbench";

it("closes on an outside pointer, preserves inside interaction and restores focus on Escape", async () => {
  const user = userEvent.setup();
  const {container} = render(<>
    <WorkbenchDisclosure label="筛选"><input aria-label="筛选内容" /></WorkbenchDisclosure>
    <button>外部按钮</button>
  </>);
  const summary = container.querySelector("summary")!;
  const details = summary.closest("details")!;
  await user.click(summary);
  await user.type(screen.getByLabelText("筛选内容"), "保留");
  expect(details.open).toBe(true);
  await user.click(screen.getByRole("button", {name:"外部按钮"}));
  expect(details.open).toBe(false);
  await user.click(summary);
  expect(screen.getByLabelText("筛选内容")).toHaveValue("保留");
  fireEvent.keyDown(screen.getByLabelText("筛选内容"), {key:"Escape"});
  expect(details.open).toBe(false);
  expect(summary).toHaveFocus();
});
