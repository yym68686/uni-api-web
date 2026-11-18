import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { useWorkbenchTools, WorkbenchDisclosure } from "./Workbench";

it("coalesces heading resizes into one frame and cancels pending work on unmount", () => {
  let notify!: () => void;
  let nextFrame!: FrameRequestCallback;
  const disconnect = vi.fn();
  const cancelFrame = vi.fn();
  const scheduleFrame = vi.fn((callback: FrameRequestCallback) => {
    nextFrame = callback;
    return 1;
  });
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: () => void) { notify = callback; }
    observe() {}
    disconnect = disconnect;
  });
  vi.stubGlobal("requestAnimationFrame", scheduleFrame);
  vi.stubGlobal("cancelAnimationFrame", cancelFrame);

  function Heading() {
    const { observeHeading, level } = useWorkbenchTools();
    return <div ref={observeHeading}>{level}</div>;
  }
  const { container, unmount } = render(<Heading />);
  const heading = container.firstElementChild!;
  Object.defineProperty(heading, "clientWidth", { value: 960 });
  notify();
  notify();
  expect(heading).toHaveTextContent("0");
  expect(scheduleFrame).toHaveBeenCalledTimes(1);
  act(() => nextFrame(0));
  expect(heading).toHaveTextContent("2");

  notify();
  unmount();
  expect(disconnect).toHaveBeenCalledTimes(1);
  expect(cancelFrame).toHaveBeenCalledWith(1);
});

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
