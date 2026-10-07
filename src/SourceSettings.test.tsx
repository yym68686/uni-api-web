import { expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { SourceSettings } from "./SourceSettings";

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
