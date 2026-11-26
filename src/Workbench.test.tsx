import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { useWorkbenchTools, WorkbenchDisclosure } from "./Workbench";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

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

it("anchors a start-aligned panel to the button and clips its height below the button", () => {
  vi.stubGlobal("innerWidth", 1400);
  vi.stubGlobal("innerHeight", 800);
  const {container} = render(<WorkbenchDisclosure label="筛选" align="start"><input /></WorkbenchDisclosure>);
  const summary = container.querySelector("summary")!;
  const panel = container.querySelector<HTMLDivElement>(".workbench-popover")!;
  const bounds = vi.spyOn(summary, "getBoundingClientRect").mockReturnValue(new DOMRect(550, 240, 80, 40));
  Object.defineProperties(panel, {offsetWidth: {value: 576}, offsetHeight: {value: 550}});
  summary.closest("details")!.open = true;
  fireEvent(window, new Event("scroll"));
  expect(panel.style.getPropertyValue("--workbench-left")).toBe("550px");
  expect(panel.style.getPropertyValue("--workbench-top")).toBe("288px");
  expect(panel.style.getPropertyValue("--workbench-bottom")).toBe("auto");
  expect(panel.style.getPropertyValue("--workbench-max-height")).toBe("500px");
  bounds.mockReturnValue(new DOMRect(1140, 680, 80, 40));
  fireEvent(window, new Event("resize"));
  expect(panel.style.getPropertyValue("--workbench-left")).toBe("812px");
  expect(panel.style.getPropertyValue("--workbench-top")).toBe("auto");
  expect(panel.style.getPropertyValue("--workbench-bottom")).toBe("128px");
});
