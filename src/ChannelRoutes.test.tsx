import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import type { ManagedChannel } from "./channelManagement";

afterEach(() => vi.unstubAllGlobals());

it("edits one native caller key with independent model positions and refuses stale writes", async () => {
  const writes: any[] = [];
  let stale = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "PATCH") {
        writes.push(JSON.parse(String(init.body)));
        return stale
          ? new Response("版本已变化", { status: 409 })
          : Response.json({ message: "已保存" });
      }
      if (input.includes("channel-options"))
        return Response.json({
          revision: "fresh-r2",
          channels: [
            { provider: "other", model: "astra" },
            { provider: "native", model: "astra" },
            { provider: "native", model: "sol" },
            { provider: "other", model: "sol" },
          ],
        });
      return Response.json({
        data: [
          {
            provider: "native",
            model: "astra",
            api_key_id: "key1",
            key_prefix: "masked-one",
            key_position: 1,
            position: 1,
          },
          {
            provider: "native",
            model: "sol",
            api_key_id: "key1",
            key_prefix: "masked-one",
            key_position: 1,
            position: 1,
          },
          {
            provider: "native",
            model: "astra",
            api_key_id: "key2",
            key_prefix: "masked-two",
            key_position: 2,
            position: 1,
          },
        ],
        unavailable_keys: [],
      });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ConfiguredChannelDialog
        item={
          {
            source_id: "source",
            source_name: "Fugue",
            provider: "native",
            name: "native",
            models: ["astra", "sol"],
          } as ManagedChannel
        }
        close={() => {}}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "编辑 Key 1" }));
  const dialog = within(
    screen.getByRole("dialog", { name: "编辑 API key · Key 1" }),
  );
  expect(await dialog.findByLabelText("native astra 的路由位置")).toHaveValue(
    "2",
  );
  expect(dialog.getByLabelText("native sol 的路由位置")).toHaveValue("1");
  await user.selectOptions(
    dialog.getByLabelText("native astra 的路由位置"),
    "1",
  );
  await user.selectOptions(dialog.getByLabelText("native sol 的路由位置"), "2");
  stale = true;
  await user.click(dialog.getByRole("button", { name: "保存更改" }));
  expect(await dialog.findByRole("alert")).toHaveTextContent("版本已变化");
  expect(writes).toEqual([
    {
      api_key_id: "key1",
      revision: "fresh-r2",
      moves: [
        { provider: "native", model: "astra", position: 1 },
        { provider: "native", model: "sol", position: 2 },
      ],
    },
  ]);
  await user.click(dialog.getByRole("button", { name: "重新读取路由" }));
  await waitFor(() =>
    expect(dialog.getByLabelText("native astra 的路由位置")).toHaveValue("2"),
  );
  expect(dialog.getByRole("button", { name: "保存更改" })).toBeDisabled();
});

it("opens an existing native copy with its aliases and preserves different positions", async () => {
  const provider = "sub2api-copy-existing",
    writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(String(init.body)));
        return Response.json({ message: "已更新" });
      }
      if (input.includes("channel-options"))
        return Response.json({
          revision: "r2",
          keys: [{ key_id: "key1", position: 1, prefix: "masked" }],
          channels: [
            { provider, model: "alias", upstream_model: "codex-auto-review" },
            { provider: "other", model: "alias" },
            { provider: "other", model: "codex-auto-review" },
            { provider, model: "codex-auto-review" },
          ],
        });
      return Response.json({
        data: [
          {
            provider,
            origin_provider: "native",
            model: "alias",
            upstream_model: "codex-auto-review",
            api_key_id: "key1",
            key_prefix: "masked",
            key_position: 1,
            position: 1,
          },
          {
            provider,
            origin_provider: "native",
            model: "codex-auto-review",
            upstream_model: "codex-auto-review",
            api_key_id: "key1",
            key_prefix: "masked",
            key_position: 1,
            position: 2,
          },
        ],
        unavailable_keys: [],
      });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ConfiguredChannelDialog
        item={
          {
            source_id: "source",
            source_name: "Fugue",
            provider: "native",
            name: "native",
            models: ["codex-auto-review"],
          } as ManagedChannel
        }
        close={() => {}}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", {
      name: `编辑 Key 1 的模型 ${provider}`,
    }),
  );
  expect(screen.getByLabelText("重命名 1 对外模型名")).toHaveValue("alias");
  expect(
    screen.getByRole("checkbox", { name: "codex-auto-review" }),
  ).toBeChecked();
  await waitFor(() =>
    expect(screen.getByLabelText("alias 的路由位置")).toBeEnabled(),
  );
  expect(screen.getByLabelText("codex-auto-review 的路由位置")).toHaveValue(
    "2",
  );
  await user.selectOptions(screen.getByLabelText("alias 的路由位置"), "2");
  await user.click(screen.getByRole("button", { name: "保存更改" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    edit_provider: provider,
    api_key_id: "key1",
    models: ["codex-auto-review"],
    model_mappings: { alias: "codex-auto-review" },
    positions: { "codex-auto-review": 2, alias: 2 },
  });
});
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
        positions: keep
          ? { "codex-auto-review": 1, "gpt-5.6-luna": 1 }
          : { "gpt-5.6-luna": 1 },
      },
    ]);
  },
);
