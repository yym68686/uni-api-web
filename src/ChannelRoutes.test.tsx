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
      if (init?.method === "POST") {
        writes.push(JSON.parse(String(init.body)));
        return stale
          ? new Response("版本已变化", { status: 409 })
          : Response.json({ message: "已保存" });
      }
      if (input.includes("channel-options"))
        return Response.json({
          revision: "fresh-r2",
          keys: [
            { key_id: "key1", position: 1, prefix: "masked-one" },
            { key_id: "key2", position: 2, prefix: "masked-two" },
          ],
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
  const dialog = within(screen.getByRole("dialog", { name: "添加到渠道" }));
  expect(await dialog.findByLabelText("astra 的路由位置")).toHaveValue("2");
  expect(dialog.getByLabelText("渠道添加位置")).toHaveValue("per-model");
  expect(dialog.getByLabelText("sol 的路由位置")).toHaveValue("1");
  for (const section of ["模型勾选", "模型重命名", "路由位置"]) {
    expect(dialog.getByRole("button", {name:`将${section}应用于所有已保存渠道`})).toBeEnabled();
  }
  expect(dialog.getByRole("button", {name:"应用全部于所有已保存渠道"})).toBeEnabled();
  await user.selectOptions(dialog.getByLabelText("astra 的路由位置"), "1");
  await user.selectOptions(dialog.getByLabelText("sol 的路由位置"), "2");
  stale = true;
  await user.click(dialog.getByRole("button", { name: "保存更改" }));
  expect(await dialog.findByRole("alert")).toHaveTextContent("版本已变化");
  expect(writes[0]).toMatchObject({
    source_id: "source",
    provider: "native",
    edit_provider: "native",
    api_key_id: "key1",
    revision: "fresh-r2",
    models: ["astra", "sol"],
    positions: { astra: 1, sol: 2 },
  });
  expect(dialog.getByRole("checkbox", { name: "astra" })).toBeChecked();
  expect(dialog.getByLabelText("添加到 API key")).toBeEnabled();
  expect(dialog.getByLabelText("渠道添加位置")).toBeEnabled();
  await user.click(dialog.getByRole("button", { name: "重新读取配置" }));
  await waitFor(() =>
    expect(dialog.getByLabelText("astra 的路由位置")).toHaveValue("2"),
  );
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
      name: "编辑 Key 1",
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
    await user.click(screen.getByRole("button", { name: "新增接入" }));
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

it("edits and adds through the selected source in a merged channel dialog", async () => {
  const writes: { path: string; body: any }[] = [];
  const members = [
    {
      source_id: "fugue",
      source_name: "Fugue",
      provider: "native",
      name: "native",
      models: ["only-fugue"],
    },
    {
      source_id: "do",
      source_name: "DigitalOcean",
      provider: "native",
      name: "native",
      models: ["only-do"],
      model_mappings: { "only-do": "upstream-do" },
    },
  ] as ManagedChannel[];
  const group = {
    ...members[0],
    source_name: "Fugue / DigitalOcean",
    members,
    models: ["only-fugue", "only-do"],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "PATCH" || init?.method === "POST") {
        writes.push({ path: input, body: JSON.parse(String(init.body)) });
        return Response.json({ message: "已保存" });
      }
      const isDo =
        input.includes("/do/") ||
        new URL(input, location.origin).searchParams.get("source_id") === "do";
      const model = isDo ? "only-do" : "only-fugue";
      if (input.includes("channel-options"))
        return Response.json({
          revision: isDo ? "do-revision" : "fugue-revision",
          keys: [{ key_id: "same-key", position: 1, prefix: "masked" }],
          channels: [
            { provider: "other", model },
            {
              provider: "native",
              model,
              upstream_model: isDo ? "upstream-do" : model,
            },
          ],
        });
      return Response.json({
        data: [
          {
            provider: "native",
            model,
            api_key_id: "same-key",
            key_position: 1,
            key_prefix: "masked",
            position: 2,
          },
        ],
        unavailable_keys: [],
      });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ConfiguredChannelDialog item={group} close={() => {}} />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.selectOptions(screen.getByLabelText("查看 uni-api 来源"), "do");
  const region = within(
    screen.getByRole("region", { name: "DigitalOcean 接入情况" }),
  );
  await user.click(await region.findByRole("button", { name: "编辑 Key 1" }));
  const editor = within(screen.getByRole("dialog", { name: "添加到渠道" }));
  expect(editor.getByLabelText("添加到 uni-api 来源")).toHaveValue("do");
  await user.selectOptions(
    await editor.findByLabelText("only-do 的路由位置"),
    "1",
  );
  await user.click(editor.getByRole("button", { name: "保存更改" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "保存更改" }),
    ).not.toBeInTheDocument(),
  );
  expect(writes[0]).toEqual({
    path: expect.stringContaining("/v1/channel-management"),
    body: {
      source_id: "do",
      provider: "native",
      edit_provider: "native",
      allow_unverified_models: true,
      api_key_id: "same-key",
      revision: "do-revision",
      models: ["only-do"],
      model_mappings: {},
      position: 1,
      positions: { "only-do": 1 },
    },
  });
  await user.click(screen.getByRole("button", { name: "新增接入" }));
  await user.selectOptions(screen.getByLabelText("添加到 uni-api 来源"), "do");
  expect(screen.getByRole("checkbox", { name: "only-do" })).toBeChecked();
  expect(
    screen.queryByRole("checkbox", { name: "only-fugue" }),
  ).not.toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByLabelText("添加到 API key")).toBeEnabled(),
  );
  await user.selectOptions(screen.getByLabelText("添加到 API key"), "same-key");
  await user.click(screen.getByRole("button", { name: "添加重命名" }));
  await user.type(screen.getByLabelText("重命名 1 对外模型名"), "public-alias");
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "per-model");
  await user.selectOptions(screen.getByLabelText("only-do 的路由位置"), "2");
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].body).toMatchObject({
    source_id: "do",
    provider: "native",
    revision: "do-revision",
    models: ["only-do"],
    model_mappings: { "public-alias": "only-do" },
    positions: { "only-do": 2, "public-alias": 1 },
  });
});

it("loads model selection for each selected caller key and saves uniform plus per-model positions", async () => {
  const writes: any[] = [];
  const rows = [
    {
      provider: "native",
      model: "astra",
      api_key_id: "key1",
      key_position: 1,
      key_prefix: "masked1",
      position: 1,
    },
    {
      provider: "native",
      model: "sol",
      api_key_id: "key1",
      key_position: 1,
      key_prefix: "masked1",
      position: 2,
    },
    {
      provider: "native",
      model: "sol",
      api_key_id: "key2",
      key_position: 2,
      key_prefix: "masked2",
      position: 1,
    },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(String(init.body)));
        return Response.json({ message: "已保存" });
      }
      if (input.includes("channel-options")) {
        const key = new URL(input, location.origin).searchParams.get(
          "api_key_id",
        );
        return Response.json({
          revision: `revision-${key}`,
          keys: [
            { key_id: "key1", position: 1, prefix: "masked1" },
            { key_id: "key2", position: 2, prefix: "masked2" },
          ],
          channels: [
            ...rows.filter((r) => r.api_key_id === key),
            { provider: "peer", model: "astra" },
            { provider: "peer", model: "sol" },
          ],
        });
      }
      return Response.json({ data: rows, unavailable_keys: [] });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ConfiguredChannelDialog
        item={
          {
            source_id: "s",
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
  await waitFor(() =>
    expect(screen.getByLabelText("添加到 API key")).toBeEnabled(),
  );
  expect(screen.getByRole("checkbox", { name: "astra" })).toBeChecked();
  await user.click(screen.getByRole("checkbox", { name: "sol" }));
  await user.selectOptions(screen.getByLabelText("添加到 API key"), "key2");
  await waitFor(() =>
    expect(screen.getByLabelText("添加到 API key")).toBeEnabled(),
  );
  expect(screen.getByRole("checkbox", { name: "sol" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "astra" })).not.toBeChecked();
  await user.click(screen.getByRole("checkbox", { name: "astra" }));
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "2");
  expect(screen.getByLabelText("astra 的路由位置")).toHaveValue("2");
  expect(screen.getByLabelText("sol 的路由位置")).toHaveValue("2");
  expect(screen.getByLabelText("astra 的路由位置")).toBeDisabled();
  expect(screen.getByLabelText("sol 的路由位置")).toBeDisabled();
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "per-model");
  expect(screen.getByLabelText("astra 的路由位置")).toBeEnabled();
  expect(screen.getByLabelText("sol 的路由位置")).toBeEnabled();
  expect(screen.getByLabelText("astra 的路由位置")).toHaveValue("2");
  await user.selectOptions(screen.getByLabelText("sol 的路由位置"), "1");
  await user.click(screen.getByRole("button", { name: "保存更改" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    source_id: "s",
    api_key_id: "key2",
    edit_provider: "native",
    revision: "revision-key2",
    models: ["sol", "astra"],
    position: 2,
    positions: { astra: 2, sol: 1 },
  });
});

it("opens unbound channels directly in the destination form and resets the key when switching sources", async () => {
  const writes: any[] = [];
  const members = ["fugue", "do"].map((source_id) => ({
    source_id,
    source_name: source_id === "fugue" ? "Fugue" : "DigitalOcean",
    provider: "native",
    name: "native",
    models: [`model-${source_id}`],
  })) as ManagedChannel[];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(String(init.body)));
        return Response.json({ message: "已添加" });
      }
      if (input.includes("channel-options")) {
        const source = new URL(input, location.origin).searchParams.get(
          "source_id",
        );
        return Response.json({
          revision: `${source}-revision`,
          keys: [
            {
              key_id: `${source}-key`,
              position: 1,
              prefix: `masked-${source}`,
            },
          ],
          channels: [{ provider: "peer", model: `model-${source}` }],
        });
      }
      return Response.json({ data: [], unavailable_keys: [] });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ConfiguredChannelDialog
        item={{ ...members[0], members }}
        close={() => {}}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await waitFor(() =>
    expect(screen.getByLabelText("添加到 API key")).toBeEnabled(),
  );
  expect(
    screen.queryByText("尚未配置到任何 API key。"),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("添加到 uni-api 来源")).toHaveValue("fugue");
  await user.selectOptions(
    screen.getByLabelText("添加到 API key"),
    "fugue-key",
  );
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "2");
  await user.selectOptions(screen.getByLabelText("添加到 uni-api 来源"), "do");
  expect(screen.getByLabelText("添加到 API key")).toHaveValue("");
  expect(screen.getByRole("button", { name: "添加到渠道" })).toBeDisabled();
  expect(
    screen.queryByRole("checkbox", { name: "model-fugue" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "model-do" })).toBeChecked();
  await waitFor(() =>
    expect(screen.getByLabelText("添加到 API key")).toBeEnabled(),
  );
  await user.selectOptions(screen.getByLabelText("添加到 API key"), "do-key");
  await waitFor(() =>
    expect(screen.getByLabelText("渠道添加位置")).toBeEnabled(),
  );
  expect(screen.getByLabelText("渠道添加位置")).toHaveValue("1");
  expect(screen.getByLabelText("model-do 的路由位置")).toBeDisabled();
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "per-model");
  expect(screen.getByLabelText("model-do 的路由位置")).toBeEnabled();
  await user.selectOptions(screen.getByLabelText("model-do 的路由位置"), "1");
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "2");
  expect(screen.getByLabelText("model-do 的路由位置")).toBeDisabled();
  expect(screen.getByLabelText("model-do 的路由位置")).toHaveValue("2");
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    source_id: "do",
    api_key_id: "do-key",
    revision: "do-revision",
    models: ["model-do"],
    position: 2,
    positions: { "model-do": 2 },
  });
});

it("native import defaults are source/model specific and can be overridden manually", async()=>{
 const checks=[{source_id:'a',provider:'native',kind:'tool-use',model:'gpt-6-sol',state:'done',fingerprint:'',result:{status:'unsupported',model:'gpt-6-sol',checked_at:1,attempts:[]}},{source_id:'b',provider:'native',kind:'tool-use',model:'gpt-6-sol',state:'done',fingerprint:'',result:{status:'supported',model:'gpt-6-sol',checked_at:1,attempts:[]}}];
 const base={provider:'native',name:'native',models:['gpt-6-sol','gpt-5.5'],account_ids:[],engine:'gpt',account_id:'',group_id:0,api_key_id:'',key_position:0,key_prefix:'',positions:{},revision:'r1',manageable:true};
 const members=[{...base,source_id:'a',source_name:'Fugue'},{...base,source_id:'b',source_name:'DigitalOcean'}];
 vi.stubGlobal('fetch',vi.fn(async(input:string)=>{
  if(input.includes('channel-options'))return Response.json({revision:'r1',keys:[{key_id:'key',position:1,prefix:'masked'}],channels:[]});
  return Response.json({data:input.endsWith('/channel-management/checks')?checks:[],labels:{},unavailable_keys:[],unavailable_sources:[]});
 }));
 render(<QueryClientProvider client={new QueryClient()}><ConfiguredChannelDialog item={{...members[0],members} as ManagedChannel} close={()=>{}}/></QueryClientProvider>);
 const user=userEvent.setup();
 const sol=await screen.findByRole('checkbox',{name:/^gpt-6-sol/});
 await waitFor(()=>expect(sol).toBeEnabled());expect(sol).not.toBeChecked();
 expect(screen.getByRole('checkbox',{name:'gpt-5.5'})).toBeChecked();
 await user.click(sol);expect(sol).toBeChecked();
 await user.selectOptions(screen.getByLabelText('添加到 uni-api 来源'),'b');
 expect(screen.getByRole('checkbox',{name:'gpt-6-sol'})).toBeChecked();
 await user.selectOptions(screen.getByLabelText('添加到 uni-api 来源'),'a');
 expect(screen.getByRole('checkbox',{name:/^gpt-6-sol/})).not.toBeChecked();
});

it("recovers checkbox models from an empty site binding and can add an unconfigured model while keeping saved aliases", async()=>{
 const item={source_id:'do',source_name:'DigitalOcean',provider:'ccttt990085',name:'ccttt990085',models:[]} as unknown as ManagedChannel;
 const rows=[{provider:item.provider,model:'gpt-5.4',upstream_model:'gpt-5.5',api_key_id:'k1',key_position:1,key_prefix:'masked',position:1},{provider:item.provider,model:'gpt-5.5',upstream_model:'gpt-5.5',api_key_id:'k1',key_position:1,key_prefix:'masked',position:1},{provider:item.provider,model:'custom-existing',upstream_model:'custom-existing',api_key_id:'k1',key_position:1,key_prefix:'masked',position:1}];
 const writes:any[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:string,init?:RequestInit)=>{
  if(init?.method==='POST'){writes.push(JSON.parse(String(init.body)));return Response.json({message:'已更新'});}
  if(input.endsWith('/channel-management'))return Response.json({data:[],unavailable_sources:[]});
  if(input.includes('channel-options'))return Response.json({revision:'r1',keys:[{key_id:'k1',position:1,prefix:'masked'}],channels:rows});
  return Response.json({data:input.endsWith('/channel-routes')?rows:[],unavailable_keys:[]});
 }));
 render(<QueryClientProvider client={new QueryClient()}><ConfiguredChannelDialog item={item} initialEdit={rows} close={()=>{}}/></QueryClientProvider>);
 const user=userEvent.setup();
 const existing=await screen.findByRole('checkbox',{name:'gpt-5.5'});
 await waitFor(()=>expect(existing).toBeEnabled());expect(existing).toBeChecked();
 expect(screen.getByRole('checkbox',{name:'custom-existing'})).toBeChecked();
 expect(screen.getByRole('checkbox',{name:'gpt-6-sol'})).not.toBeChecked();
 expect(screen.getByRole('checkbox',{name:'gpt-6-sol'})).toBeEnabled();
 expect(screen.getAllByLabelText(/对外模型名$/)).toHaveLength(1);
 expect(screen.getByLabelText('重命名 1 对外模型名')).toHaveValue('gpt-5.4');
 await user.click(screen.getByRole('checkbox',{name:'gpt-6-sol'}));
 await user.click(screen.getByRole('button',{name:'保存更改'}));
 await waitFor(()=>expect(writes).toHaveLength(1));
 expect(writes[0]).toMatchObject({models:['gpt-5.5','custom-existing','gpt-6-sol'],model_mappings:{'gpt-5.4':'gpt-5.5'},allow_unverified_models:true});
});

it("hydrates binding-only model choices from the channel inventory",async()=>{
 const item={source_id:'do',source_name:'DigitalOcean',provider:'native',name:'native',models:[]} as unknown as ManagedChannel;
 const rows=[{provider:'native',model:'gpt-5.4',upstream_model:'gpt-5.5',api_key_id:'k1',key_position:1,key_prefix:'masked',position:1},{provider:'native',model:'gpt-5.5',upstream_model:'gpt-5.5',api_key_id:'k1',key_position:1,key_prefix:'masked',position:1}];
 vi.stubGlobal('fetch',vi.fn(async(input:string)=>{
  if(input.endsWith('/channel-management'))return Response.json({data:[{...item,models:['gpt-5.4','gpt-5.5','custom-choice'],model_mappings:{'gpt-5.4':'gpt-5.5'}}],unavailable_sources:[]});
  if(input.includes('channel-options'))return Response.json({revision:'r1',keys:[{key_id:'k1',position:1,prefix:'masked'}],channels:rows});
  return Response.json({data:input.endsWith('/channel-routes')?rows:[],unavailable_keys:[]});
 }));
 render(<QueryClientProvider client={new QueryClient()}><ConfiguredChannelDialog item={item} initialEdit={rows} close={()=>{}}/></QueryClientProvider>);
 const checkbox=await screen.findByRole('checkbox',{name:'gpt-5.4'});
 await waitFor(()=>expect(checkbox).toBeChecked());
 expect(screen.getByRole('checkbox',{name:'gpt-5.5'})).toBeChecked();
 expect(screen.getByRole('checkbox',{name:'custom-choice'})).not.toBeChecked();
 expect(screen.getByRole('checkbox',{name:'custom-choice'})).toBeEnabled();
 expect(screen.queryByLabelText('重命名 1 对外模型名')).not.toBeInTheDocument();
});
