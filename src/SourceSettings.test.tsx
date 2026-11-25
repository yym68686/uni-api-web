import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { SourceSettings } from "./SourceSettings";

it("removes the legacy protocol repair entry without removing source management actions", () => {
  render(<SourceSettings sources={[{id:"one", name:"Fixture", base:"https://example.test", has_storage:true, created_at:1}]} onSaved={vi.fn()} />, {wrapper:Tooltip.Provider});
  fireEvent.click(screen.getByRole("button", { name:"管理 Fixture" }));
  expect(screen.queryByRole("menuitem", {name:/协议修复/})).not.toBeInTheDocument();
  expect(screen.getByRole("menuitem", {name:"配置托管"})).toBeVisible();
  expect(screen.getByRole("menuitem", {name:"全局设置"})).toBeVisible();
  expect(screen.getByRole("menuitem", {name:"编辑来源"})).toBeVisible();
});

it("does not present a failed source directory read as an empty directory", () => {
  render(<SourceSettings sources={[]} onSaved={vi.fn()} readError="网络断开" />, {wrapper:Tooltip.Provider});
  expect(screen.getByRole("alert")).toHaveTextContent("现有来源未改变");
  expect(screen.queryByText(/还没有来源/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", {name:"添加来源"})).toBeDisabled();
});

it("shows temporary unavailability only on the affected source in settings", () => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ enabled: true, status: "saved", channels: 0, rules: 0 })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const sources = [
    { id: "do", name: "DigitalOcean", base: "https://offline.example", has_storage: true, created_at: 1, temporarily_unavailable: true },
    { id: "primary", name: "Fugue", base: "https://online.example", has_storage: true, created_at: 1 },
  ];
  const view = render(<QueryClientProvider client={client}><Tooltip.Provider>
    <SourceSettings sources={sources} onSaved={() => {}} />
  </Tooltip.Provider></QueryClientProvider>);
  expect(within(screen.getByText("DigitalOcean").closest(".source-item") as HTMLElement).getByRole("status")).toHaveTextContent("暂时不可用");
  expect(within(screen.getByText("Fugue").closest(".source-item") as HTMLElement).queryByRole("status")).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  view.rerender(<QueryClientProvider client={client}><Tooltip.Provider>
    <SourceSettings sources={sources.map(source => ({ ...source, temporarily_unavailable: false }))} onSaved={() => {}} />
  </Tooltip.Provider></QueryClientProvider>);
  expect(screen.queryByText("暂时不可用")).not.toBeInTheDocument();
  view.unmount();
  client.clear();
  vi.unstubAllGlobals();
});
