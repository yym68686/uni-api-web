import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiChecks, groupQualityResult } from "./Sub2apiChecks";
import type { SubAccount } from "./Sub2apiChecks";
import { LatencyBadge } from "./LatencyBadge";
import { Timing } from "./ChannelMetrics";
import { SUB_MODELS } from "./sub2apiModels";
import type { SubUsage } from "./sub2apiPriceCheck";

afterEach(() => vi.unstubAllGlobals());
function mount(user = "account", client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <Sub2apiChecks user={user} />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
}
function fixtures(): SubAccount[] {
  return ["one", "two"].map((id) => ({
    id,
    name: id,
    base: `https://${id}.test`,
    email: `${id}@test.com`,
    state: "idle",
    message: "",
    synced_at: 1,
    targets: [
      {
        group_id: 1,
        name: "same-group",
        platform: "openai",
        channel: "same-channel",
        rate: 0.01,
        billing: { rate: 0.01, source: "key", checked_at: 1 },
        key_id: 42,
        active: true,
        state: "done",
        message: "",
        result: {
          model: "gpt-6-astra",
          checked_at: 1,
          verdict: id === "one" ? "pass" : "fail",
          availability: {
            status: "success",
            text: "test",
            ttft_ms: id === "one" ? 1000 : 6500,
            response_created_ms: id === "one" ? 1000 : 6500,
            duration_ms: 8000,
          },
          quality: {
            status: "success",
            text: id === "one" ? "未知" : "2024-06",
            ttft_ms: 123,
            response_created_ms: 123,
            duration_ms: 456,
          },
        },
      },
    ],
  }));
}

it.each([false,true])("imports sub2api aliases independently of originals (keep %s)", async keep => {
  const data=fixtures().slice(0,1),target=data[0].targets[0];
  target.models=[{model:"codex-auto-review",state:"done",message:"",result:{...target.result!,model:"codex-auto-review"}}];
  target.result=null;
  const writes:any[]=[];
  vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
    if(init?.method==="POST"){writes.push(JSON.parse(String(init.body)));return Response.json({message:"已添加"});}
    if(input.includes("channel-options"))return Response.json({revision:"r1",supported:true,keys:[{key_id:"k1",position:1,prefix:"masked"}],channels:[]});
    return Response.json({data:input.endsWith("/accounts")?data:input.endsWith("/sources")?[{id:"primary",name:"Fugue"}]:[],labels:{},unavailable_keys:[],unavailable_sources:[]});
  }));
  const user=userEvent.setup();mount(`sub-alias-${keep}`);
  await user.click(await screen.findByRole("button",{name:"添加到渠道"}));
  const d=within(screen.getByRole("dialog"));
  await user.click(d.getByRole("button",{name:"添加重命名"}));
  expect(d.getByRole("checkbox",{name:"codex-auto-review"})).toBeChecked();
  await user.type(d.getByLabelText("重命名 1 对外模型名"),"gpt-5.6-luna");
  if(!keep)await user.click(d.getByRole("checkbox",{name:"codex-auto-review"}));
  await user.selectOptions(d.getByLabelText("添加到 uni-api 来源"),"primary");
  await waitFor(()=>expect(d.getByLabelText("添加到 API key")).toBeEnabled());
  await user.selectOptions(d.getByLabelText("添加到 API key"),"k1");
  await waitFor(()=>expect(d.getByRole("button",{name:"添加到渠道"})).toBeEnabled());
  await user.click(d.getByRole("button",{name:"添加到渠道"}));
  await waitFor(()=>expect(writes).toHaveLength(1));
  expect(writes[0].models).toEqual(keep?["codex-auto-review"]:[]);
  expect(writes[0].model_mappings).toEqual({"gpt-5.6-luna":"codex-auto-review"});
});

it("manages initial channels with account/unassigned filters and shows every caller key route", async () => {
  const accounts = fixtures().slice(0, 1);
  const base = { kind: "configured", source_id: "primary", source_name: "Fugue", api_key_id: "", key_position: 0, key_prefix: "", positions: {}, revision: "", manageable: false };
  const configured = [
    { ...base, provider: "initial-bound", name: "initial-bound", account_id: "one", group_id: 1, account_ids: ["one"], engine: "gpt", models: ["gpt-6-astra"], binding_status: "matched", bound_keys: [{ account_id: "one", group_id: 1 }], base: "https://one.test" },
    { ...base, provider: "initial-unassigned", name: "initial-unassigned", account_id: "", group_id: 0, account_ids: [], engine: "claude", models: ["custom-model"], base: "https://unassigned.test" },
  ];
  const writes: any[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") { writes.push(JSON.parse(String(init.body))); return new Response("{}", { status: 202 }); }
    const data = input.endsWith("/channel-management") ? configured : input.endsWith("/accounts") ? accounts : input.endsWith("/channels") ? configured : input.endsWith("/sources") ? [{id:"primary",name:"Fugue"},{id:"do",name:"DigitalOcean"}] : input.endsWith("/channel-routes") ? [
      { provider:"initial-bound", model:"gpt-6-astra", api_key_id:"key-1", key_prefix:"masked-one", key_position:1, position:2 },
      { provider:"initial-bound", model:"gpt-6-astra", api_key_id:"key-2", key_prefix:"masked-two", key_position:2, position:5 },
      { provider:"initial-unassigned", model:"custom-model", api_key_id:"key-2", key_prefix:"masked-two", key_position:2, position:3 },
    ] : [];
    return new Response(JSON.stringify({ data, labels:{}, unavailable_sources:[], unavailable_keys:[] }));
  }));
  const user=userEvent.setup();
  const view=mount("management-fixture");
  await screen.findByText("initial-bound", { selector:"strong" });
  expect(screen.getByRole("heading",{name:"渠道管理"})).toBeVisible();
  expect(screen.getByRole("button",{name:"添加渠道"})).toBeVisible();
  const accountFilter=screen.getByLabelText("sub2api 账号筛选");
  await user.selectOptions(accountFilter,"one");
  expect(screen.getByText("initial-bound", {selector:"strong"})).toBeVisible();
  expect(screen.queryByText("initial-unassigned", {selector:"strong"})).not.toBeInTheDocument();
  await user.click(screen.getByRole("button",{name:"检测全部模型 · 1 个渠道"}));
  expect(writes[0].targets).toHaveLength(1);
  const boundRow=screen.getByText("initial-bound", {selector:"strong"}).closest("tr")!;
  await user.click(within(boundRow).getByRole("button",{name:"已添加 · 2 个 key"}));
  const dialog=within(screen.getByRole("dialog"));
  expect(await dialog.findByText("第 2 位")).toBeVisible();
  expect(dialog.getByText("第 5 位")).toBeVisible();
  expect(dialog.getByText("masked-one")).toBeVisible();
  expect(dialog.getByText("masked-two")).toBeVisible();
  await user.click(dialog.getByRole("button",{name:"关闭添加渠道"}));
  await user.selectOptions(accountFilter,"__unassigned__");
  expect(screen.getByText("initial-unassigned",{selector:"strong"})).toBeVisible();
  expect(screen.queryByText("initial-bound",{selector:"strong"})).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("检测模型筛选"),"custom-model");
  expect(screen.getByText("initial-unassigned",{selector:"strong"})).toBeVisible();
  await user.click(screen.getByRole("button",{name:"已添加 · 1 个 key"}));
  expect(await within(screen.getByRole("dialog")).findByText("第 3 位")).toBeVisible();
  await user.click(screen.getByRole("button",{name:"关闭添加渠道"}));
  view.unmount();
  mount("management-fixture");
  expect(await screen.findByLabelText("sub2api 账号筛选")).toHaveValue("__unassigned__");
  expect(screen.getByLabelText("检测模型筛选")).toHaveValue("custom-model");
});

it("merges the same native channel across sources in the list and import dialog", async () => {
  const channels = ["fugue", "do"].map((source_id, i) => ({
    kind: "configured", source_id, source_name: i ? "DigitalOcean" : "Fugue",
    provider: "fugue-codex", name: "fugue-codex", base: "https://same.test", engine: "gpt",
    models: i ? ["codex-auto-review", "gpt-6-astra"] : ["codex-auto-review"], account_ids: [],
  }));
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    if (input.endsWith("/channel-routes")) return Response.json({data:[{
      provider:"fugue-codex", model:"codex-auto-review", api_key_id:"same-key-id", key_prefix:"masked", key_position:1, position:input.includes("/do/") ? 3 : 1,
    }],unavailable_keys:[]});
    return Response.json({data:input.endsWith("/channel-management") ? channels : input.endsWith("/sources") ? [{id:"fugue",name:"Fugue"},{id:"do",name:"DigitalOcean"}] : [], unavailable_sources:[]});
  }));
  const user=userEvent.setup(); mount("cross-source-native");
  await screen.findByRole("button", {name:"已添加 · 2 个 key"});
  expect(screen.getAllByText("fugue-codex", {selector:"strong"})).toHaveLength(1);
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "__unassigned__");
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-6-astra");
  await user.click(screen.getByRole("button", {name:"已添加 · 2 个 key"}));
  const dialog=within(screen.getByRole("dialog"));
  expect(await dialog.findByRole("heading", {name:/已添加到 2 个 API key/})).toBeVisible();
  expect(within(dialog.getByRole("region", {name:"Fugue 接入情况"})).getByText("第 1 位")).toBeVisible();
  expect(within(dialog.getByRole("region", {name:"DigitalOcean 接入情况"})).getByText("第 3 位")).toBeVisible();
});

it("opens the import dialog with the dashboard sources while a background refresh is still pending", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const sources = [
    { id: "primary", name: "Fugue" },
    { id: "digitalocean", name: "DigitalOcean" },
  ];
  client.setQueryData(["sources", "cached-sources"], { data: sources }, { updatedAt: 1 });
  let finishSources!: (response: Response) => void;
  const fetcher = vi.fn(async (input: string) => {
    if (input.endsWith("/v1/sources")) return new Promise<Response>(resolve => { finishSources = resolve; });
    if (input.includes("/channel-options")) return new Response(JSON.stringify({ supported: true, revision: "r1", keys: [{ key_id: "target-key", position: 1, prefix: "masked" }], channels: [] }));
    return new Response(JSON.stringify({ data: input.endsWith("/accounts") ? fixtures().slice(0, 1) : [] }));
  });
  vi.stubGlobal("fetch", fetcher);
  const user = userEvent.setup();
  const view = mount("cached-sources", client);
  await user.click(await screen.findByRole("button", { name: "添加到渠道" }));
  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByRole("option", { name: "Fugue" })).toBeInTheDocument();
  expect(dialog.getByRole("option", { name: "DigitalOcean" })).toBeInTheDocument();
  expect(dialog.getByLabelText("添加到 uni-api 来源")).toBeEnabled();
  await user.selectOptions(dialog.getByLabelText("添加到 uni-api 来源"), "digitalocean");
  await waitFor(() => expect(dialog.getByLabelText("添加到 API key")).toBeEnabled());
  expect(dialog.getByRole("option", { name: "Key 1 · masked" })).toBeInTheDocument();
  expect(fetcher.mock.calls.filter(([input]) => input.endsWith("/v1/sources"))).toHaveLength(1);
  finishSources(new Response(JSON.stringify({ data: sources })));
  await waitFor(() => expect(client.isFetching({ queryKey: ["sources"] })).toBe(0));
  view.unmount();
  client.clear();
});

it("loads sources before opening the dialog and offers retry when the initial request fails", async () => {
  let finishSources!: (response: Response) => void;
  const fetcher = vi.fn(async (input: string) => {
    if (input.endsWith("/v1/sources")) return new Promise<Response>(resolve => { finishSources = resolve; });
    return new Response(JSON.stringify({ data: input.endsWith("/accounts") ? fixtures().slice(0, 1) : [] }));
  });
  vi.stubGlobal("fetch", fetcher);
  const user = userEvent.setup();
  mount("initial-sources");
  const add = await screen.findByRole("button", { name: "添加到渠道" });
  expect(fetcher.mock.calls.filter(([input]) => input.endsWith("/v1/sources"))).toHaveLength(1);
  await user.click(add);
  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByLabelText("添加到 uni-api 来源")).toBeDisabled();
  expect(dialog.getByRole("status")).toHaveTextContent("正在读取 uni-api 来源");
  finishSources(new Response("来源服务暂不可用", { status: 503 }));
  await user.click(await dialog.findByRole("button", { name: "重新读取来源" }));
  expect(fetcher.mock.calls.filter(([input]) => input.endsWith("/v1/sources"))).toHaveLength(2);
  finishSources(new Response(JSON.stringify({ data: [{ id: "primary", name: "Fugue" }, { id: "digitalocean", name: "DigitalOcean" }] })));
  await waitFor(() => expect(dialog.getByLabelText("添加到 uni-api 来源")).toBeEnabled());
  expect(dialog.getByRole("option", { name: "Fugue" })).toBeInTheDocument();
  expect(dialog.getByRole("option", { name: "DigitalOcean" })).toBeInTheDocument();
});

it("filters unit prices across pages and models, persists selection, and scopes both batch checks", async () => {
  const data = fixtures().slice(0, 1);
  const template = data[0].targets[0];
  const usage: SubUsage = {
    status: "matched", actual_cost: .01, total_cost: .1, rate_multiplier: .1,
    input_tokens: 100, output_tokens: 10, cache_read_tokens: 0, cache_creation_tokens: 0,
    input_price: 5, output_price: 30, cache_read_price: null, cache_write_price: null,
    paid_input_price: .5, paid_output_price: 3, duration_ms: 10, first_token_ms: 1,
  };
  const checks = SUB_MODELS.map(model => ({
    model, state: "done", message: "",
    result: { ...template.result!, model, availability: { ...template.result!.availability, usage } },
  }));
  data[0].targets = Array.from({ length: 27 }, (_, i) => ({
    ...template, group_id: i + 1, name: `abnormal-${i + 1}`,
    models: checks.map(check => check.model === "gpt-6-astra" ? {
      ...check, result: { ...check.result, availability: {
        ...check.result.availability, usage: { ...usage, input_price: 6 },
      } },
    } : check),
  }));
  data[0].targets.push(
    { ...template, group_id: 28, name: "all-normal", models: checks },
    { ...template, group_id: 29, name: "partial-normal", models: [checks[0]], result: null },
    { ...template, group_id: 30, name: "not-checked", models: [], result: null },
    { ...template, group_id: 31, name: "pending-price", models: checks.map(check => ({
      ...check, result: { ...check.result, availability: {
        ...check.result.availability, usage: { ...usage, status: "pending" },
      } },
    })) },
  );
  const prices = SUB_MODELS.map(model => ({ model, input: 5, output: 30, verified: true }));
  const writes: { path: string; targets: { group_id: number; models?: string[] }[] }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      writes.push({ path: new URL(input, location.origin).pathname, ...JSON.parse(String(init.body)) });
      return new Response("{}", { status: 202 });
    }
    return new Response(JSON.stringify({ data: input.endsWith("/prices") ? prices : input.endsWith("/accounts") ? data : [] }));
  }));
  const user = userEvent.setup();
  let view = mount("price-filter-fixture");
  const filter = await screen.findByLabelText("单价是否异常筛选");
  await user.selectOptions(filter, "abnormal");
  await screen.findByText("abnormal-1");
  expect(screen.queryByText("all-normal")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(2);
  await user.click(screen.getByRole("button", { name: "检测全部模型 · 27 个渠道" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets.map(t => t.group_id)).toEqual(Array.from({ length: 27 }, (_, i) => i + 1));
  expect(writes[0].targets[0].models).toEqual(SUB_MODELS);
  await user.click(screen.getByRole("button", { name: "降智检测 · 27 个渠道" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].targets.map(t => t.group_id)).toEqual(writes[0].targets.map(t => t.group_id));
  await user.selectOptions(filter, "normal");
  expect(screen.getByText("all-normal")).toBeVisible();
  expect(screen.queryByText("partial-normal")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
  await user.selectOptions(filter, "unconfirmed");
  expect(screen.getByText("partial-normal")).toBeVisible();
  expect(screen.getByText("pending-price")).toBeVisible();
  expect(screen.getByText("not-checked")).toBeVisible();
  expect(screen.queryByText("abnormal-1")).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-6-astra");
  await user.selectOptions(filter, "normal");
  expect(screen.getByText("partial-normal")).toBeVisible();
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-5.6-sol");
  expect(screen.getByText("abnormal-1")).toBeVisible();
  expect(screen.queryByText("partial-normal")).not.toBeInTheDocument();
  view.unmount();
  view = mount("price-filter-fixture");
  expect(screen.getByLabelText("单价是否异常筛选")).toHaveValue("normal");
  expect(screen.getByLabelText("检测模型筛选")).toHaveValue("gpt-5.6-sol");
  view.unmount();
  mount("price-other-user");
  expect(screen.getByLabelText("单价是否异常筛选")).toHaveValue("");
});

it("shows shared channel quality and filters it without changing the model availability result", async () => {
  const data = fixtures().slice(0, 1);
  const target = data[0].targets[0];
  target.history = { total: 4, successful: 4, passed: 2 };
  target.quality_check = { history_scope: "account_group", source_id: "source", provider: "p", model: "gpt-6-astra", verdict: "fail", text: "shared answer 20", checked_at: 200, duration_ms: 99 };
  const availability = target.result!.availability;
  expect(groupQualityResult(target)?.availability).toBe(availability);
  expect(target.result!.verdict).toBe("pass");
  vi.stubGlobal("fetch", vi.fn(async (input: string) => new Response(JSON.stringify({ data: input.includes("/accounts") ? data : [] }))));
  mount("shared-quality-fixture");
  const user = userEvent.setup();
  await screen.findByRole("button", {name:/查看 one same-group 的回复与诊断/});
  const table = screen.getAllByRole("table").find(t => t.textContent?.includes("Astra 降智"))!;
  expect(table).toHaveTextContent("50.0%");
  expect(table).toHaveTextContent("降智");
  expect(table).toHaveTextContent("可用");
  await user.click(screen.getByRole("button", {name:/查看 one same-group 的回复与诊断/}));
  expect(await screen.findByText("shared answer 20")).toBeVisible();
  expect(screen.getByRole("dialog")).toHaveTextContent("test");
});

it("runs standalone Astra quality checks for every filtered page regardless of the selected model", async () => {
  const data = fixtures();
  const first = data[0].targets[0];
  data[0].targets = Array.from({ length: 30 }, (_, i) => ({
    ...first,
    group_id: i + 1,
    name: `selected-${i + 1}`,
    models: [
      {
        model: "gpt-6-astra",
        state: "done",
        message: "",
        result: first.result,
      },
      {
        model: "gpt-5.6-sol",
        state: "done",
        message: "",
        result: { ...first.result!, model: "gpt-5.6-sol" },
      },
    ],
  }));
  data[0].targets[28].platform = "anthropic";
  data[0].targets[29].billing = { rate: 2, source: "key", checked_at: 1 };
  const writes: { path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const { pathname } = new URL(input, location.origin);
      if (init?.method === "POST") {
        writes.push({ path: pathname, body: JSON.parse(init.body as string) });
        return new Response('{"queued":true}', { status: 202 });
      }
      return new Response(
        JSON.stringify(
          pathname.endsWith("/channels") ? { data: [], labels: {} } : { data },
        ),
      );
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.type(screen.getByLabelText("搜索 sub2api 分组"), "selected");
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "one");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  await user.selectOptions(screen.getByLabelText("平台筛选"), "openai");
  await user.selectOptions(screen.getByLabelText("倍率上限筛选"), "0.01");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    3,
  );
  await user.click(
    screen.getByRole("button", { name: "降智检测 · 28 个渠道" }),
  );
  await waitFor(() =>
    expect(writes).toEqual([
      {
        path: "/analytics/v1/sub2api/quality-checks",
        body: {
          targets: Array.from({ length: 28 }, (_, i) => ({
            account_id: "one",
            group_id: i + 1,
          })),
        },
      },
    ]),
  );
});

it("refreshes Astra availability and model match from quality results without changing sibling models", async () => {
  const data = fixtures().slice(0, 1);
  const target = data[0].targets[0];
  const oldAstra = {
    ...target.result!,
    verdict: "fail",
    availability: {
      ...target.result!.availability,
      status: "error",
      ttft_ms: null,
      response_created_ms: null,
    },
  };
  const sibling = {
    ...target.result!,
    model: "gpt-5.6-sol",
    availability: {
      ...target.result!.availability,
      ttft_ms: 6500,
      response_created_ms: 6500,
      requested_model: "gpt-5.6-sol",
      response_model: "gpt-5.6-sol",
      model_match: "match" as const,
    },
  };
  target.models = [
    { model: "gpt-6-astra", state: "done", message: "", result: oldAstra },
    { model: "gpt-5.6-sol", state: "done", message: "", result: sibling },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const { pathname } = new URL(input, location.origin);
      if (pathname.endsWith("/quality-checks") && init?.method === "POST") {
        const probe = {
          status: "success",
          text: "未知",
          ttft_ms: 200,
          response_created_ms: 200,
          duration_ms: 800,
          requested_model: "gpt-6-astra",
          response_model: "gpt-5.6-sol",
          model_match: "mismatch" as const,
        };
        target.models![0].result = {
          model: "gpt-6-astra",
          checked_at: 1800000000,
          verdict: "pass",
          availability: probe,
          quality: probe,
        };
        return new Response('{"queued":true}', { status: 202 });
      }
      return new Response(
        JSON.stringify(
          pathname.endsWith("/channels") ? { data: [], labels: {} } : { data },
        ),
      );
    }),
  );
  const user = userEvent.setup();
  const first = mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  expect(screen.getByRole("table")).toHaveTextContent("6.50 s");
  await user.click(screen.getByRole("button", { name: "降智检测 · 1 个渠道" }));
  expect(await screen.findByText("不降智", { selector: "span" })).toBeVisible();
  expect(screen.getByRole("table")).toHaveTextContent("6.50 s");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  const table = screen.getByRole("table");
  expect(within(table).getByText("可用", { exact: true })).toBeVisible();
  expect(within(table).getByRole("cell", { name: /^不匹配$/ })).toBeVisible();
  expect(table).toHaveTextContent("200 ms");
  await user.click(
    screen.getByRole("button", { name: "查看 one same-group 的回复与诊断" }),
  );
  expect(
    within(screen.getByRole("dialog")).getByRole("row", {
      name: "返回模型 gpt-5.6-sol",
    }),
  ).toBeVisible();
  first.unmount();
  mount();
  expect(await screen.findByRole("cell", { name: /^不匹配$/ })).toBeVisible();
});

it("shows multipliers as numbers, filters all-page batch targets, and restores persisted results", async () => {
  const data = fixtures();
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}', { status: 202 });
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  const first = mount();
  expect(
    await screen.findByRole("columnheader", { name: "倍率" }),
  ).toBeVisible();
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  expect(await screen.findAllByText("0.01")).toHaveLength(2);
  expect(screen.getByText("不降智", { selector: "span" })).toBeVisible();
  expect(screen.getByText("降智", { selector: "span" })).toBeVisible();
  const table = screen.getByRole("table");
  expect(table.querySelectorAll(".latency-badge.fast")).toHaveLength(1);
  expect(table.querySelectorAll(".latency-badge.medium")).toHaveLength(1);
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "two");
  await user.click(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  );
  await waitFor(() =>
    expect(writes).toEqual([
      {
        targets: [{ account_id: "two", group_id: 1, models: ["gpt-6-astra"] }],
      },
    ]),
  );
  first.unmount();
  mount();
  expect(await screen.findByText("降智", { selector: "span" })).toBeVisible();
  expect(screen.getByLabelText("sub2api 账号筛选")).toHaveValue("two");
  expect(screen.getByLabelText("检测模型筛选")).toHaveValue("gpt-6-astra");
  expect(writes).toHaveLength(1);
});
it("creates an account without storing its password and supports the 2FA step", async () => {
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response(
          JSON.stringify(
            writes.length === 1
              ? { requires_2fa: true, challenge: "opaque" }
              : { queued: true },
          ),
          { status: 200 },
        );
      }
      return new Response('{"data":[]}');
    }),
  );
  const user = userEvent.setup();
  mount();
  await user.click(screen.getByRole("button", { name: "添加账号" }));
  await user.type(screen.getByLabelText("站点地址"), "https://example.com");
  await user.type(screen.getByLabelText("账号邮箱"), "me@example.com");
  await user.type(screen.getByLabelText("账号密码"), "private-password");
  await user.click(screen.getByRole("button", { name: "连接并检测" }));
  await user.type(await screen.findByLabelText("六位验证码"), "123456");
  expect(screen.queryByLabelText("账号密码")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "验证并检测" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual({ challenge: "opaque", totp_code: "123456" });
  expect(JSON.stringify(localStorage)).not.toContain("private-password");
  expect(JSON.stringify(sessionStorage)).not.toContain("private-password");
});
it("keeps busy accounts from duplicate checks and removes only after explicit selection", async () => {
  const data = fixtures();
  data[0].state = "running";
  data[0].targets[0].state = "running";
  const methods: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method) {
        methods.push(init.method);
        return new Response('{"ok":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByLabelText("检测模型筛选");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  const one = await screen.findByRole("button", {
    name: "检测 one same-group",
  });
  expect(one).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  ).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "降智检测 · 1 个渠道" }),
  ).toBeEnabled();
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "one");
  expect(
    screen.getByRole("button", { name: "降智检测 · 0 个渠道" }),
  ).toBeDisabled();
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "");
  await user.click(screen.getByRole("button", { name: "移除账号 two" }));
  expect(methods).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "确认移除" }));
  await waitFor(() => expect(methods).toEqual(["DELETE"]));
});
it("isolates row submissions by account, group and check kind, including out-of-order failures", async () => {
  const data = fixtures();
  data[0].targets.push({ ...data[0].targets[0], group_id: 2, name: "second-group" });
  const requests: { path: string; body: any; finish: (response: Response) => void }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") return new Promise<Response>(finish => {
      requests.push({ path: input, body: JSON.parse(String(init.body)), finish });
    });
    return new Response(JSON.stringify({ data: input.endsWith("/accounts") ? data : [] }));
  }));
  const user = userEvent.setup();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = mount("scoped-checks", client);
  const compact = await screen.findByRole("button", { name: "检测 one same-group 的远程压缩" });
  const tools = screen.getByRole("button", { name: "检测 one same-group 的 Tool use" });
  const sibling = screen.getByRole("button", { name: "检测 one second-group 的远程压缩" });
  const other = screen.getByRole("button", { name: "检测 two same-group" });
  await user.click(compact);
  expect(compact).toBeDisabled();
  expect(tools).toBeEnabled();
  expect(sibling).toBeEnabled();
  expect(other).toBeEnabled();
  expect(screen.getByRole("button", { name: "检测 one same-group" })).toBeEnabled();
  await user.click(tools);
  await user.click(other);
  expect(requests).toHaveLength(3);
  expect(tools).toBeDisabled();
  expect(other).toBeDisabled();
  await user.click(compact);
  expect(requests).toHaveLength(3);
  expect(requests[0].body.targets).toEqual([{ account_id: "one", group_id: 1 }]);
  expect(requests[1].path).toMatch(/tool-use-checks$/);
  requests[1].finish(new Response("检测提交失败", { status: 503 }));
  await waitFor(() => expect(tools).toBeEnabled());
  expect(compact).toBeDisabled();
  expect(other).toBeDisabled();
  data[0].state = "running";
  data[0].job_kind = "compaction";
  data[0].targets[0].state = "queued";
  data[0].targets[0].compaction_state = "running";
  requests[0].finish(new Response("{}", { status: 202 }));
  requests[2].finish(new Response("{}", { status: 202 }));
  await waitFor(() => expect(other).toBeEnabled());
  expect(compact).toBeDisabled();
  expect(tools).toBeEnabled();
  expect(sibling).toBeEnabled();
  expect(screen.getByRole("button", { name: "检测 one same-group" })).toBeEnabled();
  await user.click(sibling);
  expect(requests).toHaveLength(4);
  expect(requests[3].body.targets).toEqual([{ account_id: "one", group_id: 2 }]);
  requests[3].finish(new Response("{}", { status: 202 }));
  await waitFor(() => expect(sibling).toBeEnabled());
  view.unmount();
  client.clear();
});
it("colors only first-output p50 with exact 5s and 10s boundaries", () => {
  const { container } = render(
    <Tooltip.Provider>
      <div data-testid="badges">
        {[0, 5000, 5001, 10000, 10001, null].map((value, i) => (
          <LatencyBadge key={i} value={value} />
        ))}
      </div>
      <div data-testid="first">
        <Timing
          value={{
            p50_ms: 5001,
            p95_ms: 25000,
            last_ms: 30000,
            sample_count: 10,
            mean_ms: 10000,
          }}
        />
      </div>
      <div data-testid="wait">
        <Timing
          wait
          value={{
            p50_ms: 20000,
            p95_ms: 25000,
            last_ms: 30000,
            sample_count: 10,
            mean_ms: 10000,
          }}
        />
      </div>
    </Tooltip.Provider>,
  );
  const badges = screen.getByTestId("badges");
  expect(badges.querySelectorAll(".fast")).toHaveLength(2);
  expect(badges.querySelectorAll(".medium")).toHaveLength(2);
  expect(badges.querySelectorAll(".slow")).toHaveLength(1);
  expect(screen.getByTestId("wait").querySelector(".latency-badge")).toBeNull();
  expect(
    screen.getByTestId("first").querySelectorAll(".latency-badge"),
  ).toHaveLength(1);
  expect(container).not.toHaveTextContent("NaN");
});

it("filters models, derives rate options from other filters, applies inclusive caps and both sorts", async () => {
  const data = fixtures();
  data.push({
    ...data[1],
    id: "three",
    name: "three",
    email: "three@test.com",
  });
  data.forEach((a, i) => {
    a.targets = [
      {
        ...a.targets[0],
        billing: { rate: [0.2, 0.07, 0.1][i], source: "key", checked_at: 1 },
        models: [
          {
            model: "gpt-6-astra",
            state: "done",
            message: "",
            result: a.targets[0].result,
          },
          {
            model: "gpt-5.6-sol",
            state: "done",
            message: "",
            result: {
              ...a.targets[0].result!,
              model: "gpt-5.6-sol",
              verdict: "not_applicable",
              availability: {
                ...a.targets[0].result!.availability,
                status: i === 1 ? "error" : "success",
              },
              quality: {
                status: "not_applicable",
                text: "",
                ttft_ms: null,
                response_created_ms: null,
                duration_ms: 0,
              },
            },
          },
        ],
      },
    ];
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ data }))),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  const cap = screen.getByLabelText("倍率上限筛选");
  expect([...cap.querySelectorAll("option")].map((o) => o.value)).toEqual([
    "",
    "0.1",
    "0.2",
  ]);
  await user.selectOptions(screen.getByLabelText("倍率排序"), "asc");
  expect(
    screen.getByRole("table").querySelector("tbody tr")?.textContent,
  ).toContain("three");
  await user.selectOptions(screen.getByLabelText("倍率排序"), "desc");
  expect(
    screen.getByRole("table").querySelector("tbody tr")?.textContent,
  ).toContain("one");
  await user.selectOptions(cap, "0.1");
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    1,
  );
  expect(screen.getByRole("table")).toHaveTextContent("three");
  expect(screen.getByRole("table")).toHaveTextContent("降智");
  expect(
    screen.getByRole("columnheader", { name: "Astra 降智" }),
  ).toBeVisible();
  expect(screen.queryByText(/每个分组独立测试/)).not.toBeInTheDocument();
  expect(screen.queryByText("gpt-6-astra · Responses")).not.toBeInTheDocument();
});

it("preselects successful models and imports into the selected source key at the selected position", async () => {
  const data = fixtures().slice(0, 1),
    writes: any[] = [];
  data[0].targets[0].models = [
    {
      model: "gpt-6-astra",
      state: "done",
      message: "",
      result: data[0].targets[0].result,
    },
    {
      model: "gpt-5.6-sol",
      state: "done",
      message: "",
      result: {
        ...data[0].targets[0].result!,
        model: "gpt-5.6-sol",
        verdict: "not_applicable",
      },
    },
    {
      model: "gpt-5.5",
      state: "done",
      message: "",
      result: {
        ...data[0].targets[0].result!,
        model: "gpt-5.5",
        availability: {
          ...data[0].targets[0].result!.availability,
          status: "error",
        },
      },
    },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input);
      if (u.pathname.endsWith("/sub2api/channels")) {
        writes.push(JSON.parse(init?.body as string));
        return new Response(JSON.stringify({ message: "已临时添加至第 2 位" }));
      }
      if (u.pathname.endsWith("/sources"))
        return new Response(
          JSON.stringify({
            data: [
              { id: "source", name: "DigitalOcean", base: "https://do.test" },
            ],
          }),
        );
      if (u.pathname.endsWith("/channel-options"))
        return new Response(
          JSON.stringify({
            supported: true,
            revision: "revision-1",
            keys: [{ key_id: "key-target", position: 2, prefix: "masked" }],
            channels: [
              { provider: "existing", model: "gpt-6-astra" },
              { provider: "existing", model: "gpt-5.6-sol" },
            ],
          }),
        );
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-6-astra" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-5.6-sol" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: /gpt-5.5/ }),
  ).toBeDisabled();
  await user.selectOptions(
    within(dialog).getByLabelText("添加到 uni-api 来源"),
    "source",
  );
  await waitFor(() =>
    expect(within(dialog).getByLabelText("添加到 API key")).toBeEnabled(),
  );
  await user.selectOptions(
    within(dialog).getByLabelText("添加到 API key"),
    "key-target",
  );
  await waitFor(() =>
    expect(within(dialog).getByLabelText("渠道添加位置")).toBeEnabled(),
  );
  await user.selectOptions(within(dialog).getByLabelText("渠道添加位置"), "2");
  await user.click(within(dialog).getByRole("button", { name: "添加到渠道" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    account_id: "one",
    group_id: 1,
    source_id: "source",
    api_key_id: "key-target",
    compaction_enabled: false,
    models: ["gpt-6-astra", "gpt-5.6-sol"],
    position: 2,
    positions: {"gpt-6-astra":2,"gpt-5.6-sol":2},
    revision: "revision-1",
  });
  expect(JSON.stringify(writes)).not.toContain("secret");
  await screen.findByText("已临时添加至第 2 位");
});

it("filters compaction across all pages, queues groups independently of the selected model, and defaults imports from evidence", async () => {
  const data = fixtures().slice(0, 1);
  const template = data[0].targets[0];
  data[0].targets = Array.from({ length: 27 }, (_, i) => ({ ...template, group_id: i + 1, name: `compact-${i + 1}`,
    compaction: { status: "supported", model: "gpt-5.6-sol", checked_at: 10, attempts: [] } }));
  data[0].targets.push({ ...template, group_id: 28, name: "no-compact", compaction: { status: "unsupported", checked_at: 10, attempts: [] } },
    { ...template, group_id: 29, name: "unknown-compact" });
  const writes: { targets: {group_id: number; models?: string[]}[] }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") { writes.push(JSON.parse(String(init.body))); return new Response("{}", {status:202}); }
    return new Response(JSON.stringify({ data: input.endsWith("/accounts") ? data : [] }));
  }));
  const user = userEvent.setup();
  const view = mount("compaction-filter");
  const filter = await screen.findByLabelText("是否支持压缩筛选");
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-6-astra");
  await user.selectOptions(filter, "supported");
  await screen.findByText("compact-1");
  expect(screen.queryByText("no-compact")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", {name:"压缩检测 · 27 个渠道"}));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets.map(t => t.group_id)).toEqual(Array.from({length:27}, (_,i)=>i+1));
  expect(writes[0].targets.every(t => !t.models)).toBe(true);
  await user.click(screen.getAllByRole("button", {name:"添加到渠道"})[0]);
  expect(within(screen.getByRole("dialog")).getByRole("checkbox", {name:"开启远程压缩"})).toBeChecked();
  await user.click(screen.getByRole("button", {name:"关闭添加渠道"}));
  await user.selectOptions(filter,"unsupported");
  await user.click(screen.getByRole("button", {name:"添加到渠道"}));
  expect(within(screen.getByRole("dialog")).getByRole("checkbox", {name:"开启远程压缩"})).not.toBeChecked();
  await user.click(screen.getByRole("button", {name:"关闭添加渠道"}));
  view.unmount();
  mount("compaction-filter");
  expect(screen.getByLabelText("是否支持压缩筛选")).toHaveValue("unsupported");
});

it("filters Tool use across pages, remembers the filter, and queues only selected groups without prescribing a model", async () => {
  const data = fixtures().slice(0, 1);
  const template = data[0].targets[0];
  data[0].targets = Array.from({ length: 27 }, (_, i) => ({ ...template, group_id: i + 1, name: `tool-${i + 1}`,
    tool_use: { status: "supported", model: "gpt-5.6-sol", checked_at: 10, attempts: [] } }));
  data[0].targets.push(
    { ...template, group_id: 28, name: "no-tools", tool_use: { status: "unsupported", model: "gpt-5.6-sol", checked_at: 11,
      message: "已提供 exec，但响应返回 NO_EXEC", attempts: [{status:"unsupported", text:"NO_EXEC", requested_model:"gpt-5.6-sol", http_status:200, duration_ms:300, ttft_ms:null}] } },
    { ...template, group_id: 29, name: "tool-error", tool_use: { status: "error", checked_at: 10, attempts: [] } },
    { ...template, group_id: 30, name: "tool-unknown" },
  );
  const writes: {path:string; targets: {group_id:number;models?:string[]}[]}[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") {writes.push({path:new URL(input).pathname,...JSON.parse(String(init.body))});return new Response("{}",{status:202});}
    return new Response(JSON.stringify({ data: input.endsWith("/accounts") ? data : [] }));
  }));
  const user=userEvent.setup();
  const view=mount("tool-filter");
  const filter=await screen.findByLabelText("Tool use 是否支持筛选");
  await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-6-astra");
  await user.selectOptions(filter,"supported");
  await screen.findByText("tool-1");
  expect(screen.queryByText("no-tools")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button",{name:"下一页"}));
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(2);
  await user.click(screen.getByRole("button",{name:"Tool use 检测 · 27 个渠道"}));
  await waitFor(()=>expect(writes).toHaveLength(1));
  expect(writes[0].path).toBe("/analytics/v1/sub2api/tool-use-checks");
  expect(writes[0].targets.map(t=>t.group_id)).toEqual(Array.from({length:27},(_,i)=>i+1));
  expect(writes[0].targets.every(t=>!t.models)).toBe(true);
  await user.selectOptions(filter,"unsupported");
  expect(screen.getByText("no-tools")).toBeVisible();
  expect(screen.getByRole("button",{name:"上一页"})).toBeDisabled();
  await user.click(screen.getByRole("button",{name:"查看 one no-tools 的回复与诊断"}));
  const dialog=within(screen.getByRole("dialog"));
  expect(dialog.getByText("已提供 exec，但响应返回 NO_EXEC")).toBeVisible();
  expect(dialog.getByText("NO_EXEC")).toBeVisible();
  expect(dialog.getAllByText("gpt-5.6-sol").length).toBeGreaterThan(0);
  await user.click(screen.getByRole("button",{name:"关闭检测详情"}));
  await user.click(screen.getByRole("button",{name:"检测 one no-tools 的 Tool use"}));
  await waitFor(()=>expect(writes).toHaveLength(2));
  expect(writes[1].targets).toEqual([{account_id:"one",group_id:28}]);
  await user.selectOptions(filter,"error");expect(screen.getByText("tool-error")).toBeVisible();
  await user.selectOptions(filter,"untested");expect(screen.getByText("tool-unknown")).toBeVisible();
  view.unmount();mount("tool-filter");
  expect(screen.getByLabelText("Tool use 是否支持筛选")).toHaveValue("untested");
});

it("all-models groups channels and probes all supported models despite available/pass filters", async () => {
  const data = fixtures();
  const writes: any[] = [];
  data[0].targets[0].billing = { rate: 0.18, source: "key", checked_at: 1 };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  expect(
    screen.queryByRole("columnheader", { name: "模型" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    2,
  );
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  await user.selectOptions(screen.getByLabelText("倍率上限筛选"), "0.18");
  const table = screen.getByRole("table");
  expect(table.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(table).toHaveTextContent(`1/${SUB_MODELS.length} 可用`);
  expect(table).toHaveTextContent(`${SUB_MODELS.length - 1} 个未检测`);
  const latencyIndex = within(table)
    .getAllByRole("columnheader")
    .findIndex((h) => h.textContent === "首字延迟");
  expect(
    table.querySelector("tbody tr")?.children[latencyIndex],
  ).toHaveTextContent("—");
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    targets: [{ account_id: "one", group_id: 1, models: [...SUB_MODELS] }],
  });
  await user.click(screen.getByRole("button", { name: "检测 one same-group" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual(writes[0]);
  await user.click(
    screen.getByRole("button", { name: "查看 one same-group 的回复与诊断" }),
  );
  const details = screen.getByRole("dialog");
  for (const model of SUB_MODELS) {
    await user.selectOptions(within(details).getByLabelText("查看模型"), model);
    expect(
      within(details).getByRole("table", { name: `${model} 检测详情` }),
    ).toBeVisible();
  }
  await user.click(
    within(details).getByRole("button", { name: "关闭检测详情" }),
  );
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-6-astra" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: /gpt-5\.6-sol.*未检测/ }),
  ).toBeDisabled();
});

it("specific model uses Astra group quality and only tests the selected model", async () => {
  const data = fixtures();
  const writes: any[] = [];
  data.forEach((a) => {
    a.targets[0].models = [
      {
        model: "gpt-6-astra",
        state: "done",
        message: "",
        result: a.targets[0].result,
      },
      {
        model: "gpt-5.6-sol",
        state: "done",
        message: "",
        result: {
          ...a.targets[0].result!,
          model: "gpt-5.6-sol",
          verdict: "not_applicable",
          quality: {
            status: "not_applicable",
            text: "",
            ttft_ms: null,
            response_created_ms: null,
            duration_ms: 0,
          },
        },
      },
    ];
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  const table = screen.getByRole("table");
  expect(table.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(table).toHaveTextContent("one");
  expect(table).toHaveTextContent("gpt-5.6-sol");
  expect(table).toHaveTextContent("不降智");
  await user.click(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets).toEqual([
    { account_id: "one", group_id: 1, models: ["gpt-5.6-sol"] },
  ]);
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "");
  expect(screen.getByRole("table")).toHaveTextContent(`2/${SUB_MODELS.length} 可用`);
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].targets[0].models).toEqual([...SUB_MODELS]);
});

it("all-model batch spans all filtered pages and includes failed and untested siblings", async () => {
  const data = fixtures().slice(0, 1);
  const first = data[0].targets[0];
  data[0].targets = Array.from({ length: 28 }, (_, i) => ({
    ...first,
    group_id: i + 1,
    name: "group-" + (i + 1),
    models: [
      {
        model: "gpt-6-astra",
        state: "done",
        message: "",
        result: first.result,
      },
      {
        model: "gpt-5.5",
        state: "done",
        message: "",
        result: {
          ...first.result!,
          model: "gpt-5.5",
          availability: { ...first.result!.availability, status: "error" },
        },
      },
    ],
  }));
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "error");
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    25,
  );
  expect(screen.getAllByText(/1 个失败/).length).toBeGreaterThan(0);
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    3,
  );
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 28 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets).toHaveLength(28);
  for (const target of writes[0].targets)
    expect(target.models).toEqual([...SUB_MODELS]);
});

it("shows model matching separately from availability, including missing and historical metadata", async () => {
  const data = fixtures().slice(0, 1),
    target = data[0].targets[0];
  const statuses = [
    "match",
    "mismatch",
    "missing",
    "invalid",
    undefined,
    "unavailable",
  ] as const;
  target.models = SUB_MODELS.map((model, index) => ({
    model,
    state: "done",
    message: "",
    result: {
      ...target.result!,
      model,
      availability: {
        ...target.result!.availability,
        status: index === 5 ? "error" : "success",
        requested_model: model,
        response_model: index === 0 ? model : index === 1 ? "gpt-5.6-luna" : "",
        model_match: statuses[index],
      },
    },
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      if (new URL(input).pathname.endsWith("/sources"))
        return new Response('{"data":[]}');
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  const table = await screen.findByRole("table");
  const headers = within(table).getAllByRole("columnheader");
  const column = headers.findIndex((h) => h.textContent?.includes("模型匹配"));
  expect(column).toBeGreaterThan(0);
  const summary = table.querySelector("tbody tr")!.children[column];
  expect(summary).toHaveTextContent(`1/${SUB_MODELS.length} 匹配`);
  for (const label of ["不匹配", "未返回", "格式无效", "待补测", "无法判定"])
    expect(summary).toHaveTextContent(label);
  const expected = [
    "匹配",
    "不匹配",
    "未返回",
    "格式无效",
    "待补测",
    "无法判定",
  ];
  for (let i = 0; i < SUB_MODELS.length; i++) {
    await user.selectOptions(
      screen.getByLabelText("检测模型筛选"),
      SUB_MODELS[i],
    );
    const currentHeaders = within(table).getAllByRole("columnheader");
    const currentColumn = currentHeaders.findIndex((h) =>
      h.textContent?.includes("模型匹配"),
    );
    const cell = table.querySelector("tbody tr")!.children[currentColumn];
    expect(
      within(cell as HTMLElement).getByText(expected[i] || "待补测", { exact: true }),
    ).toBeVisible();
  }
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  const row = table.querySelector("tbody tr")!;
  const matchCell =
    row.children[
      within(table)
        .getAllByRole("columnheader")
        .findIndex((h) => h.textContent?.includes("模型匹配"))
    ];
  expect(within(matchCell as HTMLElement).getByText("不匹配")).toHaveClass(
    "fail",
  );
  await user.click(
    screen.getByRole("button", { name: "查看 one same-group 的回复与诊断" }),
  );
  const details = screen.getByRole("dialog");
  expect(
    within(details).getByRole("row", { name: "请求模型 gpt-5.6-sol" }),
  ).toBeVisible();
  expect(
    within(details).getByRole("row", { name: "返回模型 gpt-5.6-luna" }),
  ).toBeVisible();
  await user.click(
    within(details).getByRole("button", { name: "关闭检测详情" }),
  );
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  expect(
    within(screen.getByRole("dialog")).getByRole("checkbox", {
      name: "gpt-5.6-sol",
    }),
  ).toBeChecked();
});

it("filters quality thresholds from successful history and applies the selection to batch checks", async () => {
  const data = fixtures().slice(0, 1);
  const template = data[0].targets[0];
  data[0].targets = [0, 89, 90, 95, 100].map((passed, i) => ({
    ...template,
    group_id: i + 1,
    name: `probability-${passed}`,
    // Failed requests increase total, never the probability denominator.
    history: { passed, successful: 100, total: 120 },
  }));
  data[0].targets.push({ ...template, group_id: 6, name: "no-success", history: { passed: 0, successful: 0, total: 3 } });
  const writes: { body: { targets: { group_id: number }[] } }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      writes.push({ body: JSON.parse(String(init.body)) });
      return new Response("{}", { status: 202 });
    }
    return new Response(JSON.stringify(input.endsWith("/accounts") ? { data } : { data: [], labels: {}, unavailable_sources: [] }));
  }));
  const user = userEvent.setup();
  let view = mount("quality-threshold");
  await screen.findByText("probability-100");
  const filter = screen.getByLabelText("不降智概率筛选");
  expect(within(filter).getAllByRole("option")).toHaveLength(12);
  expect(screen.getByText("100.0%")).toHaveClass("quality-perfect");
  expect(screen.getByText("90.0%")).toHaveClass("quality-partial");
  expect(screen.getByText("95.0%")).toHaveClass("quality-partial");
  expect(screen.getByText("89.0%")).toHaveClass("quality-poor");
  expect(screen.getByText("0.0%")).toHaveClass("quality-poor");
  await user.selectOptions(filter, "0");
  expect(screen.queryByText("no-success")).not.toBeInTheDocument();
  expect(screen.getByText("probability-0")).toBeVisible();
  await user.selectOptions(filter, "90");
  expect(screen.queryByText("probability-89")).not.toBeInTheDocument();
  expect(screen.getByText("probability-90")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "降智检测 · 3 个渠道" }));
  expect(writes[0].body.targets.map((target) => target.group_id)).toEqual([3, 4, 5]);
  await user.selectOptions(filter, "100");
  expect(screen.queryByText("probability-95")).not.toBeInTheDocument();
  expect(screen.getByText("probability-100")).toBeVisible();
  view.unmount();
  view = mount("quality-threshold");
  expect(screen.getByLabelText("不降智概率筛选")).toHaveValue("100");
  await screen.findByText("probability-100");
  view.unmount();
  mount("quality-other");
  expect(screen.getByLabelText("不降智概率筛选")).toHaveValue("");
});

it("persists every filter per user and derives platform options from other filters", async () => {
  const data = fixtures();
  data[1].targets[0].platform = "anthropic";
  data[1].targets[0].billing!.rate = 0.2;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (input: string) =>
        new Response(
          JSON.stringify(
            input.includes("/channels")
              ? { data: [], labels: {}, unavailable_sources: [] }
              : { data },
          ),
        ),
    ),
  );
  const user = userEvent.setup();
  let view = mount("alice");
  const platforms = await screen.findByLabelText("平台筛选");
  await waitFor(() =>
    expect(within(platforms).getAllByRole("option")).toHaveLength(3),
  );
  await user.selectOptions(screen.getByLabelText("倍率上限筛选"), "0.01");
  expect(
    within(platforms).queryByRole("option", { name: "anthropic" }),
  ).toBeNull();
  await user.selectOptions(platforms, "openai");
  await user.type(screen.getByLabelText("搜索 sub2api 分组"), "one");
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "one");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  await user.selectOptions(screen.getByLabelText("倍率排序"), "desc");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  view.unmount();
  view = mount("alice");
  await screen.findByText("不降智", { selector: "span" });
  for (const [label, value] of [
    ["搜索 sub2api 分组", "one"],
    ["sub2api 账号筛选", "one"],
    ["检测模型筛选", "gpt-6-astra"],
    ["倍率上限筛选", "0.01"],
    ["倍率排序", "desc"],
    ["可用性筛选", "success"],
    ["降智筛选", "pass"],
    ["平台筛选", "openai"],
  ])
    expect(screen.getByLabelText(label)).toHaveValue(value);
  view.unmount();
  mount("bob");
  expect(screen.getByLabelText("平台筛选")).toHaveValue("");
  expect(screen.getByLabelText("搜索 sub2api 分组")).toHaveValue("");
});

it("lists imported keys, replaces exact models and deletes only the chosen binding", async () => {
  const data = fixtures().slice(0, 1);
  const writes: any[] = [];
  let installed = [
    {
      account_id: "one",
      group_id: 1,
      source_id: "source",
      source_name: "Gateway",
      api_key_id: "key1",
      key_position: 1,
      key_prefix: "masked-one",
      provider: "sub2api-test1",
      name: "one-0.01",
      models: ["gpt-6-astra", "gpt-5.6-sol"],
      positions: { "gpt-6-astra": 2, "gpt-5.6-sol": 1 },
      revision: "boot:1",
      manageable: true,
    },
    {
      account_id: "one",
      group_id: 1,
      source_id: "source",
      source_name: "Gateway",
      api_key_id: "key2",
      key_position: 2,
      key_prefix: "masked-two",
      provider: "sub2api-test2",
      name: "one-0.01",
      models: ["gpt-6-astra"],
      positions: { "gpt-6-astra": 2 },
      revision: "boot:1",
      manageable: true,
    },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      let reply: unknown = { data };
      if (input.includes("channel-options"))
        reply = {
          provider: "sub2api-test1",
          supported: true,
          manageable: true,
          revision: installed[0]?.revision || "boot:3",
          keys: [
            { key_id: "key1", position: 1, prefix: "masked-one" },
            { key_id: "key2", position: 2, prefix: "masked-two" },
          ],
          channels: [{provider:"other",model:"gpt-6-astra"},{provider:"sub2api-test1",model:"gpt-6-astra"},{provider:"sub2api-test1",model:"gpt-5.6-sol"},{provider:"other",model:"gpt-5.6-sol"}],
        };
      else if (input.endsWith("/v1/sources"))
        reply = { data: [{ id: "source", name: "Gateway" }] };
      else if (input.endsWith("/v1/sub2api/channels")) {
        if (init?.method === "PATCH") {
          const body = JSON.parse(init.body as string);
          writes.push(body);
          installed = installed
            .filter(
              (i) =>
                body.action !== "delete" || i.api_key_id !== body.api_key_id,
            )
            .map((i) => ({
              ...i,
              revision: `boot:${writes.length + 1}`,
              models: i.api_key_id === body.api_key_id ? body.models : i.models,
            }));
          reply = { message: "已保存" };
        } else reply = { data: installed, labels: {}, unavailable_sources: [] };
      }
      return new Response(JSON.stringify(reply));
    }),
  );
  mount();
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: "已添加 · 2 个 key" }),
  );
  let dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText(/masked-one/)).toBeVisible();
  expect(within(dialog).getByText(/masked-two/)).toBeVisible();
  await user.click(within(dialog).getAllByRole("button", { name: "编辑" })[0]);
  await waitFor(() =>
    expect(
      within(dialog).getByRole("button", { name: "保存更改" }),
    ).toBeEnabled(),
  );
  expect(within(dialog).getByLabelText("gpt-6-astra 的路由位置")).toHaveValue("2");
  expect(within(dialog).getByLabelText("gpt-5.6-sol 的路由位置")).toHaveValue("1");
  await user.selectOptions(within(dialog).getByLabelText("gpt-6-astra 的路由位置"),"1");
  await user.click(
    within(dialog).getByRole("checkbox", { name: /gpt-5.6-sol/ }),
  );
  await user.click(within(dialog).getByRole("button", { name: "保存更改" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    action: "replace",
    api_key_id: "key1",
    models: ["gpt-6-astra"],
    positions: {"gpt-6-astra":1},
    revision: "boot:1",
  });
  await waitFor(() =>
    expect(
      within(dialog).getAllByRole("button", { name: "删除" })[0],
    ).toBeEnabled(),
  );
  await user.click(within(dialog).getAllByRole("button", { name: "删除" })[0]);
  await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toMatchObject({
    action: "delete",
    api_key_id: "key1",
    revision: "boot:2",
  });
  await waitFor(() =>
    expect(within(dialog).queryByText(/masked-one/)).toBeNull(),
  );
  expect(within(dialog).getByText(/masked-two/)).toBeVisible();
  await user.click(
    within(dialog).getByRole("button", { name: "添加到其他 API key" }),
  );
  expect(within(dialog).getByLabelText("添加到 API key")).toHaveValue("");
});

it("keeps long replies out of the channel list and shows full fields in a dismissible dialog", async () => {
  const data = fixtures().slice(0, 1);
  const target = data[0].targets[0];
  const reply = "至少需要 21 个糖果。\n".repeat(100);
  target.result!.quality.text = reply;
  target.result!.quality.message = "完整降智诊断";
  target.result!.availability.response_created_ms = 200;
  target.message = "同步诊断信息";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ data }))),
  );
  const user = userEvent.setup();
  mount();
  const list = await screen.findByRole("table");
  expect(list).not.toHaveTextContent("至少需要 21 个糖果");
  expect(list).not.toHaveTextContent("同步诊断信息");
  const trigger = screen.getByRole("button", {
    name: "查看 one same-group 的回复与诊断",
  });
  await user.click(trigger);
  const dialog = screen.getByRole("dialog");
  const result = within(dialog).getByRole("table");
  const replyRow = within(result).getByRole("row", { name: /^降智回复/ });
  expect(within(replyRow).getByRole("cell").textContent).toBe(reply);
  expect(result).toHaveTextContent("完整降智诊断");
  expect(result).toHaveTextContent("同步诊断信息");
  expect(
    within(result).getByRole("row", { name: /^首字延迟/ }),
  ).toHaveTextContent("200 ms");
  expect(
    within(result).getByRole("row", { name: /^首个文本延迟/ }),
  ).toHaveTextContent("1.00 s");
  await user.selectOptions(
    within(dialog).getByLabelText("查看模型"),
    "gpt-5.6-sol",
  );
  expect(
    within(result).getByRole("row", { name: "最近检测 未检测" }),
  ).toBeVisible();
  expect(result).not.toHaveTextContent("至少需要 21 个糖果");
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});

it("syncs all idle accounts independently of channel filters and prevents repeat submissions", async () => {
  const data = fixtures();
  data.push({
    ...data[0],
    id: "busy",
    name: "busy",
    state: "running",
    targets: [],
  });
  const writes: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const { pathname } = new URL(input, location.origin);
      if (init?.method === "POST") {
        writes.push(pathname);
        data.forEach((account) => {
          if (account.state === "idle") account.state = "queued";
        });
        return new Response('{"queued":2}', { status: 202 });
      }
      return new Response(
        JSON.stringify(
          pathname.endsWith("/channels") ? { data: [], labels: {} } : { data },
        ),
      );
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "one");
  const button = screen.getByRole("button", { name: "一键同步并检测" });
  expect(button).toHaveTextContent("2 个账号");
  await user.click(button);
  await waitFor(() => expect(button).toBeDisabled());
  expect(writes).toEqual(["/analytics/v1/sub2api/accounts/sync"]);
  expect(
    screen.getByText("已提交 2 个账号，正在后台同步并检测。"),
  ).toBeVisible();
});

it("offers the browser helper for Turnstile and saves only its returned session", async () => {
  const writes: any[] = [];
  const onMessage = (event: MessageEvent) => {
    const { channel, id, type } = event.data || {};
    if (channel !== "uni-api-browser-login-v1") return;
    if (type === "login") expect(event.data.input.agreed).toBe(true);
    const data =
      type === "ping"
        ? { channel, id, type: "ready" }
        : type === "login"
          ? {
              channel,
              id,
              type: "result",
              auth: {
                access_token: "browser-session",
                refresh_token: "browser-refresh",
              },
            }
          : null;
    if (data)
      window.dispatchEvent(
        new MessageEvent("message", {
          data,
          origin: location.origin,
          source: window,
        }),
      );
  };
  window.addEventListener("message", onMessage);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response(
          JSON.stringify(
            writes.length === 1
              ? {
                  requires_browser: true,
                  agreement: {
                    required: true,
                    revision: "r1",
                    documents: [
                      {
                        id: "terms",
                        title: "原站协议",
                        content_md: "原站协议正文",
                      },
                    ],
                  },
                }
              : { id: "new", queued: true },
          ),
        );
      }
      return new Response('{"data":[]}');
    }),
  );
  try {
    const user = userEvent.setup();
    mount();
    await user.click(await screen.findByRole("button", { name: "添加账号" }));
    await user.type(screen.getByLabelText("站点地址"), "https://site.example");
    await user.type(screen.getByLabelText("账号邮箱"), "me@example.com");
    await user.type(screen.getByLabelText("账号密码"), "private-password");
    await user.click(screen.getByRole("button", { name: "连接并检测" }));
    const browserButton = await screen.findByRole("button", {
      name: "使用浏览器登录",
    });
    expect(screen.queryByText("原站协议")).not.toBeInTheDocument();
    expect(
      screen.queryByText("站点要求验证码？使用已登录会话接入"),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    await waitFor(() => expect(browserButton).toBeEnabled());
    await user.click(browserButton);
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual({
      name: "",
      base: "https://site.example",
      email: "me@example.com",
      access_token: "browser-session",
      refresh_token: "browser-refresh",
    });
    await waitFor(() =>
      expect(screen.queryByLabelText("账号密码")).not.toBeInTheDocument(),
    );
  } finally {
    window.removeEventListener("message", onMessage);
  }
});

it("opens account forms in a modal and restores focus when closed", async () => {
  const data = fixtures();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ data }))),
  );
  const user = userEvent.setup();
  mount();
  const opener = screen.getByRole("button", { name: "添加账号" });
  await user.click(opener);
  const dialog = screen.getByRole("dialog", { name: "添加 sub2api 账号" });
  expect(within(dialog).getByLabelText("站点地址")).toBeVisible();
  expect(document.querySelector(".sub-accounts .sub-account-form")).toBeNull();
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(opener).toHaveFocus();
  await user.click(screen.getAllByRole("button", { name: "重新登录" })[0]);
  const login = screen.getByRole("dialog", { name: "重新登录站点账号" });
  expect(within(login).getByLabelText("站点地址")).toHaveValue(
    "https://one.test",
  );
  expect(within(login).getByLabelText("账号密码")).toHaveValue("");
  await user.click(within(login).getByRole("button", { name: "关闭账号窗口" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("shows account wallet balances using the shared thresholds", async () => {
  const amounts = [-0.01, 0, 50, 50.01, null];
  const data = amounts.map((_, index) => ({
    ...fixtures()[0],
    id: String(index),
    name: `site-${index}`,
    targets: [],
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const path = new URL(input, location.origin).pathname;
      const match = path.match(/accounts\/(\d+)\/balance$/);
      return new Response(
        JSON.stringify(
          match
            ? { amount: amounts[Number(match[1])], status: "ok", checked_at: 1 }
            : { data },
        ),
      );
    }),
  );
  mount();
  for (const [index, tone] of [
    "negative",
    "balance-warning",
    "balance-warning",
    "balance-positive",
    "muted",
  ].entries()) {
    const name = await screen.findByText(`site-${index}`, {
      selector: "strong",
    });
    await waitFor(() =>
      expect(
        name.closest("article")?.querySelector(".sub-account-balance .amount"),
      ).toHaveClass(tone),
    );
  }
  expect(screen.getByText("$0.00")).toHaveClass("balance-warning");
});

it("shows per-request charges and pre-multiplier anomalies, and leaves anomalous imports unchecked", async () => {
  const data = fixtures().slice(0, 1);
  const target = data[0].targets[0];
  const usage = {
    status: "matched" as const, log_id: 101, request_id: "client:site-receipt", actual_cost: .00008,
    total_cost: .0008, rate_multiplier: .1, input_tokens: 100, output_tokens: 10,
    cache_read_tokens: 0, cache_creation_tokens: 0, input_price: 5, output_price: 30,
    cache_read_price: null, cache_write_price: null, paid_input_price: .5, paid_output_price: 3,
    duration_ms: 8000, first_token_ms: 200,
  };
  target.result!.availability = { ...target.result!.availability, id: "probe-say-test", usage };
  target.result!.quality = { ...target.result!.quality, id: "probe-quality", usage: { ...usage, log_id: 102, actual_cost: .0007 } };
  target.models = [
    { model: "gpt-6-astra", state: "done", message: "", result: target.result },
    { model: "gpt-5.6-sol", state: "done", message: "", result: { ...target.result!, model: "gpt-5.6-sol", availability: { ...target.result!.availability, usage: { ...usage, input_price: 4, output_price: 20 } } } },
  ];
  const prices = [
    { model: "gpt-6-astra", input: 10, output: 50, verified: true },
    { model: "gpt-5.6-sol", input: 4, output: 20, verified: true },
  ];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const path = new URL(input).pathname;
    if (path.endsWith("/prices")) return new Response(JSON.stringify({ data: prices }));
    if (path.endsWith("/sub2api/channels")) return new Response(JSON.stringify({ data: [], labels: {}, unavailable_sources: [] }));
    return new Response(JSON.stringify({ data }));
  }));
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-6-astra");
  const table = screen.getByRole("table");
  expect(await within(table).findByText("异常", { exact: true })).toBeVisible();
  expect(within(table).getByText("$5/$30")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "查看 one same-group 的回复与诊断" }));
  const detail = screen.getByRole("dialog");
  expect(within(detail).getByRole("row", { name: /^可用性实际扣费/ })).toHaveTextContent("$0.00008");
  expect(within(detail).getByRole("row", { name: /^降智实际扣费/ })).toHaveTextContent("$0.0007");
  expect(within(detail).getByRole("row", { name: /^可用性倍率前单价/ })).toHaveTextContent("$5/$30");
  expect(within(detail).getByRole("row", { name: /^可用性实付单价/ })).toHaveTextContent("$0.5/$3");
  expect(within(detail).getByRole("row", { name: /^可用性价格设置/ })).toHaveTextContent("$10/$50");
  await user.click(within(detail).getByRole("button", { name: "关闭检测详情" }));
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const dialog = screen.getByRole("dialog");
  const abnormal = within(dialog).getByRole("checkbox", { name: /gpt-6-astra/ });
  expect(abnormal).not.toBeChecked();
  expect(abnormal).toBeEnabled();
  expect(within(dialog).getByRole("checkbox", { name: "gpt-5.6-sol" })).toBeChecked();
  await user.click(abnormal);
  expect(abnormal).toBeChecked();
});

it("remembers model settings per user and uses them for batch and row probes", async () => {
  const writes: any[] = [];
  const data = fixtures().slice(0, 1);
  vi.stubGlobal("fetch", vi.fn(async (_input: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      writes.push(JSON.parse(init.body as string));
      return new Response('{"queued":true}');
    }
    return new Response(JSON.stringify({ data }));
  }));
  const user = userEvent.setup();
  let view = mount("model-owner");
  await screen.findByRole("table");
  await user.click(screen.getByRole("button", { name: "检测模型设置" }));
  let dialog = within(screen.getByRole("dialog"));
  expect(dialog.getAllByRole("checkbox")).toHaveLength(22);
  for (const box of dialog.getAllByRole("checkbox")) expect(box).toBeChecked();
  await user.click(dialog.getByRole("button", { name: "清空" }));
  expect(dialog.getByRole("button", { name: "保存设置" })).toBeDisabled();
  for (const model of ["glm-5.3", "gemini-3.1-pro", "claude-opus-5"])
    await user.click(dialog.getByRole("checkbox", { name: model }));
  await user.click(dialog.getByRole("button", { name: "保存设置" }));
  const buttonName = "检测已选 3 个模型 · 1 个渠道";
  await user.click(screen.getByRole("button", { name: buttonName }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ targets: [{ account_id: "one", group_id: 1, models: ["glm-5.3", "gemini-3.1-pro", "claude-opus-5"] }] });
  await user.click(screen.getByRole("button", { name: "检测 one same-group" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual(writes[0]);
  view.unmount();
  view = mount("model-owner");
  await screen.findByRole("table");
  expect(screen.getByRole("button", { name: buttonName })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "检测模型设置" }));
  dialog = within(screen.getByRole("dialog"));
  expect(dialog.getAllByRole("checkbox").filter(box => (box as HTMLInputElement).checked)).toHaveLength(3);
  await user.click(dialog.getByRole("button", { name: "全选" }));
  await user.click(dialog.getByRole("button", { name: "取消" }));
  expect(screen.getByRole("button", { name: buttonName })).toBeEnabled();
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-6-astra");
  expect(screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "检测 one same-group" })).toBeDisabled();
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "claude-opus-5");
  await user.click(screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }));
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes[2].targets[0].models).toEqual(["claude-opus-5"]);
  view.unmount();
  mount("different-owner");
  await screen.findByRole("table");
  await user.click(screen.getByRole("button", { name: "检测模型设置" }));
  for (const box of within(screen.getByRole("dialog")).getAllByRole("checkbox")) expect(box).toBeChecked();
});

it("displays native first-response latency without inventing response.created events", async () => {
  const data = fixtures().slice(0, 1);
  const target = data[0].targets[0];
  target.models = ["gemini-3.1-pro", "claude-opus-5"].map((model) => ({
    model, state: "done", message: "", result: { ...target.result!, model,
      availability: { ...target.result!.availability, protocol: model.startsWith("gemini") ? "gemini" as const : "messages" as const, response_created_ms: null, first_response_ms: 120, ttft_ms: 450 },
    },
  }));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data }))));
  const user = userEvent.setup();
  mount("native-latency");
  await screen.findByRole("table");
  for (const model of ["gemini-3.1-pro", "claude-opus-5"]) {
    await user.selectOptions(screen.getByLabelText("检测模型筛选"), model);
    await user.click(screen.getByRole("button", { name: "查看 one same-group 的回复与诊断" }));
    const dialog = within(screen.getByRole("dialog"));
    expect(dialog.getByRole("row", { name: /首字延迟/ })).toHaveTextContent("120 ms");
    expect(dialog.getByRole("row", { name: /首个文本延迟/ })).toHaveTextContent("450 ms");
    expect(dialog.getByRole("row", { name: /首个文本延迟/ })).not.toHaveTextContent("response.output_text.delta");
    await user.click(dialog.getByRole("button", { name: "关闭检测详情" }));
  }
});
