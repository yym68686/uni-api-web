import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { LoadingIndicator } from "./LoadingIndicator";
import { PageLoading, TableLoading } from "./PageLoading";
import { RouteWorkspaceLoading } from "./RouteWorkspaceLoading";

it("renders one textless, named indicator for each blocking surface", () => {
  render(<>
    <main><PageLoading view="overview" /></main>
    <section><TableLoading kind="requests" /></section>
    <dl><dt>余额</dt><dd><LoadingIndicator size="inline" label="查询余额" /></dd></dl>
  </>);
  expect(screen.getAllByRole("status")).toHaveLength(3);
  for (const name of ["加载统计指标", "加载请求日志", "查询余额"]) {
    const indicator = screen.getByRole("status", { name });
    expect(indicator.textContent).toBe("");
    expect(indicator.querySelector(".spinner")).toHaveAttribute("aria-hidden", "true");
  }
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});

it("fills the channel import dialog body while waiting and keeps retry and close available", async () => {
  const user = userEvent.setup();
  const close = vi.fn();
  const retry = vi.fn();
  const props = { context: "tokunexai · gemini", label: "读取完整模型检测结果", close, retry };
  const view = render(<RouteWorkspaceLoading {...props} />);
  const dialog = within(screen.getByRole("dialog", { name: "添加到渠道" }));
  const indicator = dialog.getByRole("status", { name: props.label });
  expect(indicator).toHaveClass("route-loading-body", "loading-indicator--fill");
  expect(indicator.textContent).toBe("");
  expect(dialog.getByRole("button", { name: "关闭模型编辑" })).toBeEnabled();
  view.rerender(<RouteWorkspaceLoading {...props} error="模型检测结果读取失败" />);
  expect(dialog.queryByRole("status", { name: props.label })).not.toBeInTheDocument();
  expect(dialog.getByRole("alert")).toHaveTextContent("模型检测结果读取失败");
  await user.click(dialog.getByRole("button", { name: "重试" }));
  expect(retry).toHaveBeenCalledOnce();
  view.rerender(<RouteWorkspaceLoading {...props} />);
  await user.click(dialog.getByRole("button", { name: "关闭模型编辑" }));
  expect(close).toHaveBeenCalledOnce();
});
