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
  email: "fixture@example.com",
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
function mount(checkedTarget: SubTarget = target) {
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
          target={checkedTarget}
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
  await screen.findByRole("option", { name: /^Key 2 · masked-two · 近 24 小时/ });
  await user.selectOptions(screen.getByLabelText("查看 API key"), "key2");
  expect(screen.getByRole("table")).toHaveTextContent("native-only");
});

it("keeps saved site models checked but blocks untested models in edit mode",async()=>{
 const writes:any[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:string,init?:RequestInit)=>{
  if(init?.method==='PATCH'){writes.push(JSON.parse(String(init.body)));return Response.json({message:'已更新'});}
  if(input.includes('channel-options'))return Response.json({revision:'r1',supported:true,manageable:true,provider:'temp',keys:[{key_id:'key1',position:1,prefix:'masked'}],channels:[{provider:'temp',model:'claude-opus-5-5'}]});
  return Response.json({data:[],unavailable_keys:[],unavailable_sources:[]});
 }));
 mount();const user=userEvent.setup();
 await user.click(await screen.findByRole('button',{name:'编辑'}));
 const saved=await screen.findByRole('checkbox',{name:/^claude-opus-5-5/});
 expect(saved).toBeChecked();
 const added=screen.getByRole('checkbox',{name:/^gpt-6-sol/});
 expect(added).toBeDisabled();expect(added).not.toBeChecked();
 await user.click(added);
 await waitFor(()=>expect(screen.getByRole('button',{name:'保存更改'})).toBeEnabled());
 await user.click(screen.getByRole('button',{name:'保存更改'}));
 await waitFor(()=>expect(writes).toHaveLength(1));
 expect(writes[0]).toMatchObject({action:'replace',models:['claude-opus-5-5']});
});

it("uses the same availability restrictions for site edits and aliases",async()=>{
 const writes:any[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:string,init?:RequestInit)=>{
  if(init?.method==='PATCH'){writes.push(JSON.parse(String(init.body)));return Response.json({message:'已更新'});}
  if(input.includes('channel-options'))return Response.json({revision:'r1',supported:true,manageable:true,provider:'temp',keys:[{key_id:'key1',position:1,prefix:'masked'}],channels:[{provider:'temp',model:'claude-opus-5-5'}]});
  return Response.json({data:[],unavailable_keys:[],unavailable_sources:[]});
 }));
 const models=[['claude-opus-5-5','error'],['gpt-6-sol','error'],['gpt-5.5','success']].map(([model,status])=>({model,state:'done',message:'',result:{model,availability:{status}}}));
 mount({...target,models} as SubTarget);
 const user=userEvent.setup();
 await user.click(await screen.findByRole('button',{name:'编辑'}));
 await waitFor(()=>expect(screen.getByRole('button',{name:'保存更改'})).toBeEnabled());
 const saved=screen.getByRole('checkbox',{name:'claude-opus-5-5'});
 expect(saved).toBeChecked();expect(saved).toBeEnabled();
 const failed=screen.getByRole('checkbox',{name:'gpt-6-sol'});
 expect(failed).toBeDisabled();expect(failed.closest('label')).toHaveTextContent('检测失败');
 await user.click(screen.getByRole('checkbox',{name:'gpt-5.5'}));
 await user.click(screen.getByRole('button',{name:'添加重命名'}));
 expect(within(screen.getByLabelText('重命名 1 上游模型')).getByRole('option',{name:'gpt-6-sol'})).toBeDisabled();
 await user.click(screen.getByRole('button',{name:'删除重命名 1'}));
 await user.click(saved);expect(saved).toBeDisabled();
 await user.click(screen.getByRole('button',{name:'保存更改'}));
 await waitFor(()=>expect(writes).toHaveLength(1));
 expect(writes[0].models).toEqual(['gpt-5.5']);
});

it("recovers an expired site session in place without slow refetches or losing the import draft", async () => {
  const writes: { path: string; body: any }[] = [];
  let importAttempts = 0;
  let postFailureReads = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      writes.push({ path: input, body: JSON.parse(String(init.body)) });
      if (input.endsWith("/accounts/account/login")) return Response.json({ authenticated: true, queued: false });
      importAttempts++;
      return importAttempts === 1
        ? new Response("站点保存的登录会话已失效，请重新登录站点后继续", { status: 400 })
        : Response.json({ message: "添加成功" });
    }
    if (input.includes("channel-options")) return Response.json({
      revision: "r1", supported: true, manageable: true,
      keys: [{ key_id: "key2", position: 2, prefix: "masked-two" }],
      channels: [{ provider: "peer", model: "gpt-6-sol" }, { provider: "peer", model: "public-alias" }],
    });
    if (importAttempts > 0) {
      postFailureReads++;
      // Unrelated source reads may stay pending. Authentication recovery must
      // never wait for them or unnecessarily start them after a rejected add.
      return new Promise<Response>(() => {});
    }
    return Response.json({ data: [], unavailable_keys: [], unavailable_sources: [] });
  }));
  const models = ["gpt-6-sol", "gpt-5.5"].map(model => ({
    model, state: "done", message: "", result: { model, availability: { status: "success" } },
  }));
  mount({ ...target, models } as SubTarget);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /新增接入/ }));
  await user.selectOptions(screen.getByLabelText("添加到 uni-api 来源"), "primary");
  await user.selectOptions(screen.getByLabelText("添加到 API key"), "key2");
  await waitFor(() => expect(screen.getByRole("button", { name: "添加到渠道" })).toBeEnabled());
  await user.click(screen.getByRole("checkbox", { name: "gpt-5.5" }));
  await user.click(screen.getByRole("button", { name: "添加重命名" }));
  await user.selectOptions(screen.getByLabelText("重命名 1 上游模型"), "gpt-6-sol");
  await user.type(screen.getByLabelText("重命名 1 对外模型名"), "public-alias");
  await user.selectOptions(screen.getByLabelText("渠道添加位置"), "per-model");
  await user.selectOptions(screen.getByLabelText("gpt-6-sol 的路由位置"), "2");
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const recover = await screen.findByRole("button", { name: "重新登录站点" });
  expect(recover).toBeEnabled();
  expect(screen.getByRole("button", { name: "关闭添加渠道" })).toBeEnabled();
  expect(postFailureReads).toBe(0);
  await user.click(recover);
  let login = screen.getByRole("dialog", { name: "重新登录站点账号" });
  await user.click(within(login).getByRole("button", { name: "取消" }));
  expect(screen.getByLabelText("重命名 1 对外模型名")).toHaveValue("public-alias");
  await user.click(screen.getByRole("button", { name: "重新登录站点" }));
  login = screen.getByRole("dialog", { name: "重新登录站点账号" });
  await user.type(within(login).getByLabelText("账号密码"), "fixture-password");
  await user.click(within(login).getByRole("button", { name: "登录并返回" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "重新登录站点账号" })).not.toBeInTheDocument());
  expect(screen.getByLabelText("添加到 uni-api 来源")).toHaveValue("primary");
  expect(screen.getByLabelText("添加到 API key")).toHaveValue("key2");
  expect(screen.getByRole("checkbox", { name: "gpt-6-sol" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "gpt-5.5" })).not.toBeChecked();
  expect(screen.getByLabelText("重命名 1 对外模型名")).toHaveValue("public-alias");
  expect(screen.getByLabelText("gpt-6-sol 的路由位置")).toHaveValue("2");
  expect(postFailureReads).toBe(0);
  expect(writes[1].path).toMatch(/\/accounts\/account\/login$/);
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  await waitFor(() => expect(importAttempts).toBe(2));
  expect(writes[2].body).toEqual(writes[0].body);
  expect(JSON.stringify(localStorage)).not.toContain("fixture-password");
  expect(JSON.stringify(sessionStorage)).not.toContain("fixture-password");
});

it("checks live group state without blocking source selection and disables stale successful models", async () => {
  let resolveAccess!: (response: Response) => void;
  let recovered = false;
  const writes: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") { writes.push(input); return Response.json({}); }
    if (input.endsWith("/groups/1/access")) {
      if (recovered) return Response.json({ available: true, status: "available" });
      return new Promise<Response>(resolve => { resolveAccess = resolve; });
    }
    if (input.includes("channel-options")) return Response.json({
      revision: "r1", supported: true, manageable: true,
      keys: [{ key_id: "key2", position: 2, prefix: "masked-two" }],
      channels: [],
    });
    return Response.json({ data: [], unavailable_keys: [], unavailable_sources: [] });
  }));
  const models = [{ model: "gpt-6-sol", state: "done", message: "", result: { model: "gpt-6-sol", availability: { status: "success" } } }];
  mount({ ...target, active: true, models } as SubTarget);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /新增接入/ }));
  await user.selectOptions(screen.getByLabelText("添加到 uni-api 来源"), "primary");
  await user.selectOptions(screen.getByLabelText("添加到 API key"), "key2");
  expect(screen.getByLabelText("添加到 API key")).toBeEnabled();
  resolveAccess(Response.json({ available: false, status: "disabled", message: "站点已停用该分组，无法新增接入。" }));
  await screen.findByText("站点已停用该分组，无法新增接入。");
  expect(screen.getByRole("button", { name: "添加到渠道" })).toBeDisabled();
  expect(screen.getByRole("checkbox", { name: "gpt-6-sol" })).toBeDisabled();
  expect(writes).toHaveLength(0);
  recovered = true;
  await user.click(screen.getByRole("button", { name: "重新检查分组状态" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "添加到渠道" })).toBeEnabled());
  expect(screen.getByRole("checkbox", { name: "gpt-6-sol" })).toBeChecked();
  expect(screen.getByLabelText("添加到 API key")).toHaveValue("key2");
  expect(writes).toHaveLength(0);
});
