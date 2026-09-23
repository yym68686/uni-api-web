import { afterEach, expect, it, vi } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ChannelBindingActions,
  RemoveConfiguredBinding,
} from "./ChannelBindingActions";

afterEach(() => vi.unstubAllGlobals());
it("shows identical actions and deletes only the selected native binding with a reviewed revision", async () => {
  const target = {
    source_id: "do",
    source_name: "DigitalOcean",
    provider: "native",
    name: "配置渠道",
    model: "astra",
  };
  const writes: any[] = [];
  let revision = "r1";
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        writes.push(JSON.parse(String(init.body)));
        return writes.length === 1
          ? new Response("配置版本已变化", { status: 409 })
          : Response.json({ message: "已删除" });
      }
      if (input.includes("channel-options")) {
        reads++;
        expect(input).toContain("source_id=do");
        expect(input).toContain("api_key_id=k2");
        return Response.json({ revision, channels: [{ provider: "native" }] });
      }
      return Response.json({});
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelBindingActions
        target={target}
        onEdit={() => {}}
        remove={
          <RemoveConfiguredBinding target={target} keyId="k2" keyPosition={2} />
        }
      />
    </QueryClientProvider>,
  );
  expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual([
    "编辑",
    "渠道设置",
    "删除",
  ]);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "删除" }));
  const dialog = within(screen.getByRole("dialog", { name: "删除渠道接入" }));
  expect(dialog.getByText(/DigitalOcean · Key 2/)).toBeVisible();
  await waitFor(() =>
    expect(dialog.getByRole("button", { name: "确认删除" })).toBeEnabled(),
  );
  expect(writes).toHaveLength(0);
  await user.click(dialog.getByRole("button", { name: "确认删除" }));
  expect(await dialog.findByRole("alert")).toHaveTextContent("配置版本已变化");
  expect(writes[0]).toEqual({
    source_id: "do",
    api_key_id: "k2",
    provider: "native",
    revision: "r1",
  });
  revision = "r2";
  await user.click(dialog.getByRole("button", { name: "重新读取配置" }));
  await waitFor(() => expect(reads).toBe(2));
  await waitFor(() =>
    expect(dialog.getByRole("button", { name: "确认删除" })).toBeEnabled(),
  );
  await user.click(dialog.getByRole("button", { name: "确认删除" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(writes[1]).toEqual({ ...writes[0], revision: "r2" });
});
