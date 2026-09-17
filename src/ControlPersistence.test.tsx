import { it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ControlPersistence } from "./ControlPersistence";
it("defaults on and persists the per-source switch through the server", async () => {
  let enabled = true;
  const writes: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === "PUT") {
        const value = JSON.parse(String(init.body));
        enabled = value.enabled;
        writes.push(value);
      }
      return new Response(
        JSON.stringify({
          enabled,
          status: enabled ? "saved" : "disabled",
          channels: 3,
          rules: 4,
          saved_at: 1,
          restored_at: null,
        }),
      );
    }),
  );
  const mount = () =>
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ControlPersistence source="primary" name="Fugue" />
      </QueryClientProvider>,
    );
  const view = mount();
  const user = userEvent.setup();
  const toggle = screen.getByRole("switch", { name: "Fugue 保留临时配置" });
  expect(toggle).toHaveAttribute("aria-checked", "true");
  await waitFor(() => expect(toggle).toBeEnabled());
  await user.click(toggle);
  await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
  expect(writes).toEqual([{ enabled: false }]);
  view.unmount();
  mount();
  await waitFor(() =>
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "false"),
  );
});
