import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiImport } from "./Sub2apiImport";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import type { ConsoleSourcesQuery } from "./consoleSources";

afterEach(() => vi.unstubAllGlobals());
const account = {
  id: "account",
  name: "站点",
  base: "https://site.test",
  targets: [],
} as unknown as SubAccount;
const target = {
  group_id: 1,
  name: "Claude Max",
  models: [],
  result: null,
} as unknown as SubTarget;
const imported = (source: string): InstalledChannel => ({
  account_id: "account",
  group_id: 1,
  source_id: source,
  source_name: source === "primary" ? "Fugue" : "DigitalOcean",
  api_key_id: "key1",
  key_position: 1,
  key_prefix: "masked-one",
  provider: "temp",
  name: `站点接入-${source}`,
  models: ["claude-opus-5-5"],
  positions: { "claude-opus-5-5": 2 },
  revision: "r1",
  manageable: true,
});
const configured = (source: string): InstalledChannel => ({
  ...imported(source),
  models: ["claude-opus-5-5", "native-only", "do-only"],
  kind: "configured",
  provider: "native",
  name: `原生-${source}`,
  api_key_id: "",
  key_position: 0,
  key_prefix: "",
  binding_status: "matched",
  bound_keys: [
    {
      account_id: "account",
      account_name: "站点",
      base: "https://site.test",
      group_id: 1,
      remote_key_id: 42,
    },
  ],
});
function mount() {
  const data = [
    imported("primary"),
    imported("do"),
    configured("primary"),
    configured("do"),
  ];
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Tooltip.Provider>
        <Sub2apiImport
          account={account}
          target={target}
          imports={
            {
              data: { data, labels: {}, unavailable_sources: [] },
              isPending: false,
              isError: false,
            } as unknown as SubImportsQuery
          }
          sources={
            {
              data: {
                data: [
                  { id: "primary", name: "Fugue" },
                  { id: "do", name: "DigitalOcean" },
                ],
              },
            } as ConsoleSourcesQuery
          }
          close={() => {}}
        />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
}
const route = (
  provider: string,
  model: string,
  key = "key1",
  position = 1,
) => ({
  provider,
  model,
  api_key_id: key,
  key_position: key === "key1" ? 1 : 2,
  key_prefix: key === "key1" ? "masked-one" : "masked-two",
  position,
});
it("uses one source/key selector and one table for native and imported routes, preserving edit scope", async () => {
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "PATCH" || init?.method === "POST") {
        writes.push({ path: input, body: JSON.parse(String(init.body)) });
        return Response.json({});
      }
      if (input.includes("channel-options"))
        return Response.json({
          revision: "fresh",
          keys: [
            { key_id: "key1", position: 1, prefix: "masked-one" },
            { key_id: "key2", position: 2, prefix: "masked-two" },
          ],
          channels: [
            { provider: "peer", model: "native-only" },
            { provider: "native", model: "native-only" },
          ],
        });
      if (input.includes("/primary/"))
        return Response.json({
          data: [
            { ...route("temp", "claude-opus-5-5"), origin_provider: "native" }, // same binding discovered twice
            route("native", "claude-opus-5-5"), // distinct provider must remain
            route("native", "native-only", "key2", 2),
            route("unrelated", "must-not-show", "key3"),
          ],
          unavailable_keys: [],
        });
      return Response.json({
        data: [route("native", "do-only")],
        unavailable_keys: [],
      });
    }),
  );
  mount();
  const user = userEvent.setup();
  await screen.findByRole("button", { name: "已接入 · 3 个 Key" });
  expect(screen.getAllByLabelText("查看 uni-api 来源")).toHaveLength(1);
  expect(screen.getAllByLabelText("查看 API key")).toHaveLength(1);
  expect(screen.queryByText(/已有渠道 ·/)).not.toBeInTheDocument();
  for (const row of document.querySelectorAll(".route-binding-provider")) {
    expect(
      within(row as HTMLElement)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["编辑", "渠道设置", "删除"]);
  }
  const table = screen.getByRole("table", { name: "当前模型路由" });
  expect(within(table).getAllByRole("row")).toHaveLength(3);
  expect(table).toHaveTextContent("站点接入-primary");
  expect(table).toHaveTextContent("原生-primary");
  expect(table).toHaveTextContent("第 2 位");
  expect(table).toHaveTextContent("第 1 位");
  expect(table).not.toHaveTextContent("do-only");
  expect(
    screen.getByLabelText("查看 API key").querySelectorAll("option"),
  ).toHaveLength(2);
  await user.selectOptions(screen.getByLabelText("查看 API key"), "key2");
  expect(screen.getByRole("table")).toHaveTextContent("native-only");
  expect(screen.getByRole("table")).not.toHaveTextContent("claude-opus-5-5");
  expect(screen.getByRole("button", { name: "删除" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "渠道设置" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "编辑" }));
  const edit = within(screen.getByRole("dialog", { name: "添加到渠道" }));
  await waitFor(() =>
    expect(edit.getByLabelText("native-only 的路由位置")).toBeEnabled(),
  );
  expect(edit.getByRole("checkbox", { name: "native-only" })).toBeChecked();
  expect(edit.getByRole("button", { name: "添加重命名" })).toBeVisible();
  expect(edit.getByLabelText("添加到 API key")).toHaveValue("key2");
  expect(edit.getByLabelText("渠道添加位置")).toHaveValue("per-model");
  await user.selectOptions(edit.getByLabelText("native-only 的路由位置"), "1");
  await user.click(edit.getByRole("button", { name: "保存更改" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    path: expect.stringContaining("/v1/channel-management"),
    body: {
      source_id: "primary",
      provider: "native",
      edit_provider: "native",
      api_key_id: "key2",
      revision: "fresh",
      models: ["native-only"],
      model_mappings: {},
      position: 1,
      positions: { "native-only": 1 },
    },
  });
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "保存更改" }),
    ).not.toBeInTheDocument(),
  );
  await user.selectOptions(screen.getByLabelText("查看 uni-api 来源"), "do");
  expect(screen.getByLabelText("查看 API key")).toHaveValue("key1");
  expect(screen.getByRole("table")).toHaveTextContent("do-only");
  expect(screen.getByRole("table")).not.toHaveTextContent("native-only");
  await user.click(screen.getAllByRole("button", { name: "删除" })[0]);
  let confirmation = within(screen.getByRole("dialog", { name: "删除渠道接入" }));
  expect(confirmation.getByText("DigitalOcean · Key 1")).toBeVisible();
  expect(confirmation.getByText("站点接入-do")).toBeVisible();
  await user.click(confirmation.getByRole("button", { name: "取消" }));
  expect(writes).toHaveLength(1);
  expect(screen.queryByRole("dialog", { name: "删除渠道接入" })).not.toBeInTheDocument();
  await user.click(screen.getAllByRole("button", { name: "删除" })[0]);
  await user.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].body).toMatchObject({
    action: "delete",
    source_id: "do",
    api_key_id: "key1",
    account_id: "account",
    group_id: 1,
  });
});
it("keeps imported bindings visible when native routes fail and can retry the shared selector", async () => {
  let failed = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      if (input.includes("/primary/"))
        return failed
          ? new Response("路由暂不可用", { status: 503 })
          : Response.json({
              data: [route("native", "native-only", "key2")],
              unavailable_keys: [],
            });
      return Response.json({ data: [], unavailable_keys: [] });
    }),
  );
  mount();
  const user = userEvent.setup();
  expect(await screen.findByRole("alert")).toHaveTextContent("路由暂不可用");
  expect(screen.getByRole("table")).toHaveTextContent("claude-opus-5-5");
  expect(screen.getAllByLabelText("查看 API key")).toHaveLength(1);
  failed = false;
  await user.click(screen.getByRole("button", { name: "重新读取路由" }));
  await screen.findByRole("option", { name: "Key 2 · masked-two" });
  await user.selectOptions(screen.getByLabelText("查看 API key"), "key2");
  expect(screen.getByRole("table")).toHaveTextContent("native-only");
});
