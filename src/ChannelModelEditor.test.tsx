import { expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { ChannelModels } from "./ChannelModels";
import type { Channel } from "./types";
import type { SubImportsQuery } from "./sub2apiImports";

function setup(site = false, saveError = false, failedGlobalImports = false) {
  const provider = site ? "sub2api-site" : "sub2api-copy-native";
  const row = {
    source_id: "do",
    source_name: "DigitalOcean",
    provider,
    model: "saved",
    eligible: true,
  } as Channel;
  const saved = {
    source_id: "do",
    source_name: "DigitalOcean",
    provider,
    account_id: "a",
    group_id: 7,
    api_key_id: "key2",
    key_position: 2,
    key_prefix: "masked-two",
    models: ["saved", "alias"],
    model_mappings: { alias: "saved" },
    positions: { saved: 2, alias: 1 },
    revision: "r1",
    manageable: true,
  };
  const imports = {
    data: { data: site ? [saved] : [], labels: {}, unavailable_sources: [] },
    isPending: false,
    refetch: vi.fn(),
  } as unknown as SubImportsQuery;
  const bindingData = imports.data;
  if (failedGlobalImports) Object.assign(imports, {data: undefined, error: new DOMException("signal timed out", "TimeoutError"), isError: true});
  const result = (model: string, status = "success") => ({
    model,
    checked_at: 10,
    availability: { status, protocol: "responses", usage: { status: "matched", input_tokens: 100, output_tokens: 10, input_price: 2, output_price: 10 } },
  });
  const modelChecks = ["saved", "available", "failed"].map((model) => ({
    model,
    state: "done",
    message: "",
    result: result(model, model === "failed" ? "error" : "success"),
  }));
  const rows = [
    { provider: "peer", model: "saved", upstream_model: "saved" },
    { provider, model: "saved", upstream_model: "saved" },
    { provider, model: "alias", upstream_model: "saved" },
  ];
  const routes = rows
    .filter((r) => r.provider === provider)
    .map((r, i) => ({
      ...r,
      origin_provider: "native",
      api_key_id: "key2",
      key_position: 2,
      key_prefix: "masked-two",
      position: i ? 1 : 2,
    }));
  const inventory = ["primary", "do"].map((source_id) => ({
    source_id,
    source_name: source_id === "do" ? "DigitalOcean" : "Fugue",
    provider: "native",
    name: "Native",
    engine: "gpt",
    kind: "configured",
    models: ["saved", "available", "failed"],
    probe_fingerprint: "f",
    account_ids: [],
  }));
  const writes: { path: string; body: any }[] = [];
  let inventoryFailed = false;
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    if (["PATCH", "POST"].includes(init?.method || "")) {
      writes.push({ path: input, body: JSON.parse(String(init?.body)) });
      return saveError ? new Response("版本冲突", {status:409}) : Response.json({ message: "已保存" });
    }
    const url = new URL(input, location.origin),
      p = url.pathname;
    if (p.endsWith("/prices")) return Response.json({ data: [{model:"saved",input:1,output:10,verified:true}] });
    if (p.endsWith("/sources"))
      return Response.json({
        data: [
          { id: "primary", name: "Fugue" },
          { id: "do", name: "DigitalOcean" },
        ],
      });
    if (p.endsWith("/sub2api/accounts") || p.includes("/groups/"))
      return Response.json({
        data: site
          ? [
              {
                id: "a",
                name: "Site",
                base: "https://fixture.test",
                targets: [
                  {
                    group_id: 7,
                    name: "Group",
                    platform: "openai",
                    active: true,
                    models: modelChecks,
                  },
                ],
              },
            ]
          : [],
      });
    if (p.endsWith("/sub2api/channels")) return Response.json(bindingData);
    if (p.endsWith("/channel-management"))
      return inventoryFailed
        ? new Response("读取失败", { status: 503 })
        : Response.json({ data: inventory, unavailable_sources: [] });
    if (p.endsWith("/channel-management/checks"))
      return Response.json({
        data: modelChecks.map((c) => ({
          ...c,
          kind: "model",
          provider: "native",
          source_id: "do",
          fingerprint: "f",
        })),
      });
    if (p.endsWith("/channel-options"))
      return Response.json({
        revision: "r1",
        manageable: true,
        supported: true,
        provider,
        keys: [{ key_id: "key2", position: 2, prefix: "masked-two" }],
        channels: rows,
      });
    if (p.endsWith("/channel-routes"))
      return Response.json({
        data: p.includes("/do/") ? routes : [],
        unavailable_keys: [],
      });
    return Response.json({ data: [], labels: {}, unavailable_sources: [] });
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <ChannelModels
          row={row}
          imports={imports}
          catalog={[row]}
          keyId="do::key2"
          session="fixture"
        />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  return {
    client,
    fetchMock,
    writes,
    user: userEvent.setup(),
    failInventory: () => {
      inventoryFailed = true;
    },
  };
}

for (const site of [false, true])
  it(`opens the full ${site ? "site" : "native copy"} editor at the drawer's exact source/key`, async () => {
    const app = setup(site);
    expect(
      app.fetchMock.mock.calls.some(([path]) =>
        path.includes("channel-options"),
      ),
    ).toBe(false);
    await app.user.click(screen.getByRole("button", { name: "编辑可用模型" }));
    await screen.findByRole("dialog", { name: "添加到渠道" });
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "available" })).toBeEnabled(),
    );
    expect(screen.getByLabelText("添加到 uni-api 来源")).toHaveValue("do");
    expect(screen.getByLabelText("添加到 API key")).toHaveValue("key2");
    expect(screen.getByRole("checkbox", { name: "saved" })).toBeChecked();
    await waitFor(() => expect(screen.getByRole("checkbox", { name: "saved" }).closest("label")).toHaveTextContent("单价异常"));
    expect(screen.getByRole("checkbox", { name: "failed" })).toBeDisabled();
    expect(screen.getByLabelText("saved 的路由位置")).toHaveValue("2");
    expect(screen.getByLabelText("重命名 1 对外模型名")).toHaveValue("alias");
    for (const section of ["模型勾选", "模型重命名", "路由位置"])
      expect(
        screen.getByRole("button", {
          name: `将${section}应用于所有已保存渠道`,
        }),
      ).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
    ).toBeEnabled();
    await app.user.click(screen.getByRole("checkbox", { name: "available" }));
    await app.client.invalidateQueries({ queryKey: ["channel-routes", "do"] });
    expect(screen.getByRole("checkbox", { name: "available" })).toBeChecked();
    await app.user.click(screen.getByRole("button", { name: "保存更改" }));
    await waitFor(() => expect(app.writes).toHaveLength(1));
    expect(app.writes[0].body).toMatchObject({
      source_id: "do",
      api_key_id: "key2",
      models: ["saved", "available"],
      model_mappings: { alias: "saved" },
      positions: { saved: 2, alias: 1, available: 1 },
    });
    expect(app.writes[0].path).toContain(
      site ? "/sub2api/channels" : "/channel-management",
    );
  });

it("preserves the native editor draft when background inventory refresh fails", async () => {
  const app = setup();
  await app.user.click(screen.getByRole("button", { name: "编辑可用模型" }));
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "available" })).toBeEnabled(),
  );
  await app.user.click(screen.getByRole("checkbox", { name: "available" }));
  app.failInventory();
  await app.client.invalidateQueries({ queryKey: ["channel-management"] });
  expect(screen.getByRole("checkbox", { name: "available" })).toBeChecked();
  expect(
    screen.getByRole("dialog", { name: "添加到渠道" }),
  ).toBeInTheDocument();
});

for (const site of [false, true])
  it(`closes the ${site ? "site" : "native"} editor on write success while refreshes remain pending`, async () => {
    const app = setup(site);
    await app.user.click(screen.getByRole("button", {name:"编辑可用模型"}));
    await waitFor(() => expect(screen.getByRole("button", {name:"保存更改"})).toBeEnabled());
    const invalidate = vi.spyOn(app.client, "invalidateQueries").mockImplementation(() => new Promise(() => {}));
    await app.user.click(screen.getByRole("button", {name:"保存更改"}));
    await waitFor(() => expect(screen.queryByRole("dialog", {name:"添加到渠道"})).not.toBeInTheDocument());
    expect(app.writes).toHaveLength(1);
    expect(invalidate).toHaveBeenCalled();
    invalidate.mockRestore();
  });
for (const site of [false, true])
  it(`preserves the ${site ? "site" : "native"} editor on write failure and releases the close button`, async () => {
    const app = setup(site, true);
    await app.user.click(screen.getByRole("button", {name:"编辑可用模型"}));
    await waitFor(() => expect(screen.getByRole("button", {name:"保存更改"})).toBeEnabled());
    const invalidate = vi.spyOn(app.client, "invalidateQueries").mockImplementation(() => new Promise(() => {}));
    await app.user.click(screen.getByRole("button", {name:"保存更改"}));
    expect(await screen.findByText("版本冲突")).toBeVisible();
    expect(screen.getByRole("dialog", {name:"添加到渠道"})).toBeVisible();
    expect(screen.getByRole("button", {name:/关闭添加渠道|关闭已有渠道/})).toBeEnabled();
    invalidate.mockRestore();
  });

it("opens the selected editor when the global billing-enriched imports read times out", async () => {
  const app = setup(true, false, true);
  await app.user.click(screen.getByRole("button", {name:"编辑可用模型"}));
  await waitFor(() => expect(screen.getByRole("button", {name:"保存更改"})).toBeEnabled());
  expect(screen.getByRole("checkbox", {name:"available"})).toBeEnabled();
  expect(screen.queryByText("signal timed out")).not.toBeInTheDocument();
  const urls = app.fetchMock.mock.calls.map(([input]) => new URL(input,location.origin));
  expect(urls.some(url => url.pathname.endsWith("/sub2api/accounts"))).toBe(false);
  expect(urls.filter(url => url.pathname.endsWith("/sub2api/channels")).map(url => Object.fromEntries(url.searchParams)))
    .toEqual([{source_id:"do",view:"bindings"}]);
  expect(urls.some(url => url.pathname.endsWith("/accounts/a/groups/7/details"))).toBe(true);
  expect(app.writes).toHaveLength(0);
});
