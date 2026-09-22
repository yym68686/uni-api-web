import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import type { ManagedChannel } from "./channelManagement";

afterEach(() => vi.unstubAllGlobals());
it.each([false, true])(
  "keeps original selection independent from alias (keep original %s)",
  async (keep) => {
    const writes: any[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        if (init?.method === "POST") {
          writes.push(JSON.parse(String(init.body)));
          return Response.json({ message: "已添加" });
        }
        if (input.includes("channel-options"))
          return Response.json({
            revision: "r1",
            keys: [{ key_id: "k1", position: 1, prefix: "masked" }],
            channels: [],
          });
        return Response.json({
          data: [
            {
              provider: "fugue-codex",
              model: "codex-auto-review",
              api_key_id: "k1",
              key_prefix: "masked",
              key_position: 1,
              position: 1,
            },
            {
              provider: "fugue-codex",
              model: "other-model",
              api_key_id: "k1",
              key_prefix: "masked",
              key_position: 1,
              position: 2,
            },
          ],
          unavailable_keys: [],
        });
      }),
    );
    const item = {
      source_id: "primary",
      source_name: "Fugue",
      provider: "fugue-codex",
      name: "fugue-codex",
      models: ["codex-auto-review", "gpt-5.6-luna"],
    } as ManagedChannel;
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ConfiguredChannelDialog item={item} close={() => {}} />
      </QueryClientProvider>,
    );
    expect(
      await screen.findByRole("heading", { name: "已添加到 1 个 API key" }),
    ).toBeVisible();
    await user.click(
      screen.getByRole("button", { name: "添加到 API key / 模型重命名" }),
    );
    await user.click(screen.getByRole("button", { name: "添加重命名" }));
    expect(
      screen.getByRole("checkbox", { name: "codex-auto-review" }),
    ).toBeChecked();
    await user.type(
      screen.getByLabelText("重命名 1 对外模型名"),
      "gpt-5.6-luna",
    );
    expect(screen.getByRole("alert")).toHaveTextContent("重复");
    await user.click(screen.getByRole("checkbox", { name: "gpt-5.6-luna" }));
    if (!keep)
      await user.click(
        screen.getByRole("checkbox", { name: "codex-auto-review" }),
      );
    await user.selectOptions(screen.getByLabelText("添加到 API key"), "k1");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "添加到渠道" })).toBeEnabled(),
    );
    const row = screen
      .getByLabelText("重命名 1 上游模型")
      .closest(".model-alias-row")!;
    expect(within(row as HTMLElement).getAllByRole("combobox")[0]).toHaveValue(
      "codex-auto-review",
    );
    expect(row.textContent?.indexOf("原来的名字")).toBeLessThan(
      row.textContent?.indexOf("重命名后的名字")!,
    );
    await user.click(screen.getByRole("button", { name: "添加到渠道" }));
    await screen.findByRole("status");
    expect(writes).toEqual([
      {
        source_id: "primary",
        provider: "fugue-codex",
        api_key_id: "k1",
        revision: "r1",
        models: keep ? ["codex-auto-review"] : [],
        model_mappings: { "gpt-5.6-luna": "codex-auto-review" },
        position: 1,
      },
    ]);
  },
);
