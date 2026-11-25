
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { inMenuTriangle, IntentMenu } from "./IntentMenu";

const panel = { left: 60, right: 260, top: 5, bottom: 105 };
it("preserves a diagonal path to either near edge, not movement away", () => {
  expect(inMenuTriangle({ x: 0, y: 50 }, { x: 40, y: 80 }, panel, false)).toBe(true);
  expect(inMenuTriangle({ x: 0, y: 50 }, { x: 40, y: 105 }, panel, false)).toBe(false);
  expect(inMenuTriangle({ x: 320, y: 45 }, { x: 280, y: 65 }, panel, true)).toBe(true);
  expect(inMenuTriangle({ x: 320, y: 45 }, { x: 340, y: 45 }, panel, true)).toBe(false);
});

afterEach(() => vi.useRealTimers());
function setup() {
  const run = vi.fn();
  render(<><IntentMenu label="管理来源" context="来源 A" items={[
    { label: "配置托管", children: [{ label: "完整配置托管", run }] },
    { label: "协议修复", run },
  ]} /><button>页面外部</button></>);
  return { trigger: screen.getByRole("button", { name: "管理来源" }), run };
}

it("opens immediately by keyboard, returns focus by Escape and executes once", () => {
  const { trigger, run } = setup();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  const parent = screen.getByRole("menuitem", { name: "配置托管" });
  expect(parent).toHaveFocus();
  fireEvent.keyDown(parent, { key: "ArrowRight" });
  const child = screen.getByRole("menuitem", { name: "完整配置托管" });
  expect(child).toHaveFocus();
  fireEvent.keyDown(child, { key: "Escape" });
  expect(screen.queryByRole("menuitem", { name: "完整配置托管" })).not.toBeInTheDocument();
  expect(parent).toHaveFocus();
  fireEvent.keyDown(parent, { key: "ArrowRight" });
  fireEvent.click(screen.getByRole("menuitem", { name: "完整配置托管" }));
  expect(run).toHaveBeenCalledTimes(1);
  expect(trigger).toHaveFocus();
});

it("delays both hover activities without restarting the open timer as the pointer moves", () => {
  vi.useFakeTimers();
  const { trigger } = setup();
  fireEvent.click(trigger);
  const parent = screen.getByRole("menuitem", { name: "配置托管" });
  fireEvent.pointerEnter(parent, { pointerType: "mouse", clientX: 10, clientY: 50 });
  act(() => vi.advanceTimersByTime(100));
  fireEvent.pointerMove(parent, { pointerType: "mouse", clientX: 12, clientY: 54 });
  expect(parent).toHaveAttribute("aria-expanded", "false");
  act(() => vi.advanceTimersByTime(41));
  expect(parent).toHaveAttribute("aria-expanded", "true");
  fireEvent.pointerEnter(screen.getByRole("menuitem", { name: "协议修复" }), { pointerType: "mouse", clientX: 4, clientY: 200 });
  act(() => vi.advanceTimersByTime(239));
  expect(parent).toHaveAttribute("aria-expanded", "true");
  act(() => vi.advanceTimersByTime(2));
  expect(parent).toHaveAttribute("aria-expanded", "false");
});
