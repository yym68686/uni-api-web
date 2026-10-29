import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiChecks } from "./Sub2apiChecks";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { ManagementPage, ManagementSelection } from "./managementQuery";
import { SUB_MODELS } from "./sub2apiModels";
import { controlRequest } from "./api";

afterEach(() => vi.unstubAllGlobals());
const target = (n: number): SubTarget => ({
  group_id: n,
  name: `Channel ${n}`,
  channel: "",
  platform: "openai",
  rate: 1,
  key_id: n,
  active: true,
  state: "idle",
  message: "",
  result: null,
  models: [],
});
const account: SubAccount = {
  id: "owned",
  name: "Owned site",
  base: "https://fixture.test",
  email: "",
  state: "idle",
  message: "",
  synced_at: 1,
  targets: [],
};
const idleProgress=()=>Response.json({data:[{...account,progress:{total:0,completed:0,queued:0,running:0,interrupted:0,running_models:[],groups:[]}}],read_at_ms:Date.now()});
const rows = Array.from({ length: 61 }, (_, i) => ({
  id: `row-${i + 1}`,
  account,
  target: target(i + 1),
  checks: [],
  selected: undefined,
  accountIds: [account.id],
}));
function page(offset = 0): ManagementPage {
  return {
    format: "management-page-v1",
    revision: "current",
    read_at_ms: 1,
    rows: rows.slice(offset, offset + 25),
    total: 61,
    eligible_count: 61,
    offset,
    page_size: 25,
    selection_token: "signed-all",
    next_cursor: offset + 25 < 61 ? String(offset + 25) : undefined,
    previous_cursor: offset ? String(offset - 25) : undefined,
    accounts: [account],
    rates: [],
    platforms: ["openai"],
    models: SUB_MODELS,
    detection_models: SUB_MODELS,
    context: {
      prices: [],
      imports: [],
      routes: {},
      fingerprints: Object.fromEntries(
        rows.slice(offset, offset + 25).map((row) => [row.id, []]),
      ),
      fingerprint_models: [],
    },
  };
}
const selection = (): ManagementSelection => ({
  format: "management-selection-v1",
  revision: "current",
  read_at_ms: 2,
  rows,
  eligible: rows,
  total: 61,
  eligible_count: 61,
  input: {
    accounts: [{ ...account, targets: rows.map((row) => row.target) }],
    channels: [],
    imports: [],
    checks: [],
    routes: {},
    unavailable_sources: [],
    unavailable_imports: [],
  },
  prices: [],
});
function mount(user: string) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Tooltip.Provider>
        <Sub2apiChecks user={user} />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
}

it.each([true,false])("starts live sync progress without clearing rows, changing the page or repeating the submission (detect=%s)", async detect=>{
 const user=userEvent.setup();let pageReads=0,progressReads=0,posts=0;
 let finishProgress!:(response:Response)=>void;
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input);
  if(url.pathname.endsWith(detect ? "/sync-selected" : "/sync-only")){
   posts++;expect(init?.method).toBe("POST");return Response.json({queued:1},{status:202});
  }
  if(url.pathname.endsWith("/accounts/progress")){
   progressReads++;expect(JSON.parse(url.searchParams.get("accounts")!)).toEqual([account.id]);
   return new Promise<Response>(resolve=>{finishProgress=resolve;});
  }
  if(url.pathname.endsWith("/channel-management/query")){pageReads++;return Response.json(page(Number(url.searchParams.get("cursor")||0)));}
  if(url.pathname.endsWith("/sources"))return Response.json({data:[]});
  if(url.pathname.endsWith("/balance"))return Response.json({status:"unavailable"});
  throw Error(`Unexpected read ${url.pathname}`);
 }));
 mount("sync-stable-page");
 await screen.findByText("Channel 1",{selector:"strong"});
 await user.click(screen.getByRole("button",{name:"下一页"}));
 await screen.findByText("Channel 26",{selector:"strong"});
 const row=screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"});
 const button=screen.getByRole("button",{name:detect ? "一键同步并检测" : "一键同步"});
 await user.click(button);
 await waitFor(()=>expect(progressReads).toBe(2));
 expect(pageReads).toBe(2);expect(posts).toBe(1);expect(button).toBeDisabled();
 expect(screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"})).toBe(row);
 expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
 await act(async()=>{finishProgress(Response.json({data:[{...account,state:"running",job_kind:"sync",sync_models:["gpt-5.5"],active_groups:1,completed_groups:0,progress:{total:32,completed:3,queued:27,running:2,interrupted:0,running_models:["gpt-5.5"],groups:[]}}],read_at_ms:2}));});
 await screen.findByText("模型进度：3/32 完成 · 2 检测中 · 27 排队");
 expect(screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"})).toBe(row);
 expect(button).toBeDisabled();expect(pageReads).toBe(2);expect(posts).toBe(1);
 await user.click(screen.getByRole("button",{name:"刷新渠道管理"}));
 await waitFor(()=>expect(pageReads).toBe(3));
 await screen.findByText("Channel 26",{selector:"strong"});
 expect(pageReads).toBe(3);expect(posts).toBe(1);
});

it("renders just the current 25 rows and fetches all 61 before a batch, without old full-list reads", async () => {
  const reads: string[] = [],
    writes: { targets: unknown[] }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input);
      if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
      reads.push(url.pathname);
      if (init?.method === "POST") {
        writes.push(JSON.parse(String(init.body)));
        return Response.json({ queued: 61 });
      }
      if (url.pathname.endsWith("/channel-management/query"))
        return Response.json(
          url.searchParams.has("selection")
            ? selection()
            : page(Number(url.searchParams.get("cursor") || 0)),
        );
      if (url.pathname.endsWith("/sources")) return Response.json({ data: [] });
      if (url.pathname.endsWith("/balance"))
        return Response.json({ status: "unavailable" });
      throw Error(`Unexpected old full read: ${url.pathname}`);
    }),
  );
  mount("paging-wire");
  const user = userEvent.setup();
  await screen.findByText("Channel 1", { selector: "strong" });
  expect(screen.getAllByRole("row")).toHaveLength(26);
  expect(
    screen.queryByText("Channel 26", { selector: "strong" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("61 个渠道可检测")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("Channel 26", { selector: "strong" });
  expect(
    screen.queryByText("Channel 1", { selector: "strong" }),
  ).not.toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 61 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets).toHaveLength(61);
  expect(
    reads.filter(
      (path) =>
        path.endsWith("/accounts") ||
        path.endsWith("/channel-management") ||
        path.endsWith("/model-fingerprints"),
    ),
  ).toEqual([]);
});

it("cancels an obsolete full-selection read without submitting after the filter changes", async () => {
  let finish!: (value: Response) => void, signal: AbortSignal | undefined;
  const writes: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input);
      if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
      if (init?.method === "POST") {
        writes.push(init.body);
        return Response.json({ queued: 61 });
      }
      if (url.pathname.endsWith("/channel-management/query")) {
        if (url.searchParams.has("selection")) {
          signal = init?.signal as AbortSignal;
          return new Promise<Response>((r) => {
            finish = r;
          });
        }
        return Response.json(page());
      }
      return Response.json({ data: [] });
    }),
  );
  mount("paging-cancel");
  const user = userEvent.setup();
  await screen.findByText("Channel 1", { selector: "strong" });
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 61 个渠道" }),
  );
  await waitFor(() => expect(signal).toBeDefined());
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "gpt-5.5");
  expect(signal!.aborted).toBe(true);
  await act(async () => {
    finish(Response.json(selection()));
  });
  await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
  expect(writes).toEqual([]);
});

it("retries a changed cursor at the requested page instead of resetting to page one", async () => {
  let failed = false;
  const cursors: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input);
      if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
      if (url.pathname.endsWith("/channel-management/query")) {
        const cursor = url.searchParams.get("cursor") || "";
        cursors.push(cursor);
        if (cursor && !failed) {
          failed = true;
          return Response.json(
            { error: "渠道已变化，请刷新" },
            { status: 409 },
          );
        }
        return Response.json(page(Number(cursor || 0)));
      }
      return Response.json({ data: [] });
    }),
  );
  mount("paging-stale");
  const user = userEvent.setup();
  await screen.findByText("Channel 1", { selector: "strong" });
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("暂时无法读取渠道");
  expect(failed).toBe(true);
  expect(screen.queryByText("没有匹配的渠道")).not.toBeInTheDocument();
  await user.click(
    within(screen.getByRole("alert")).getByRole("button", { name: "重试" }),
  );
  await screen.findByText("Channel 26", { selector: "strong" });
  expect(cursors).toEqual(["", "25", "25"]);
});

it("allows sync-only with no selected models and never falls back to detection on an old backend",async()=>{
 const writes:string[]=[];
 const {saveSubModels}=await import("./sub2apiModels");saveSubModels("sync-only-empty",[]);
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const path=new URL(input).pathname;
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(init?.method){writes.push(path);return new Response("404 page not found",{status:404});}
  if(path.endsWith("/channel-management/query"))return Response.json(page());
  if(path.endsWith("/sources"))return Response.json({data:[]});
  if(path.endsWith("/balance"))return Response.json({status:"unavailable"});
  throw Error(`Unexpected read ${path}`);
 }));
 mount("sync-only-empty");const user=userEvent.setup();
 await screen.findByText("Channel 1",{selector:"strong"});
 expect(screen.getByRole("button",{name:"一键同步并检测"})).toBeDisabled();
 expect(screen.getByRole("button",{name:"一键同步"})).toBeEnabled();
 await user.click(screen.getByRole("button",{name:"一键同步"}));
 await screen.findByText("同步检测服务待更新，请稍后重试。");
 expect(writes).toEqual(["/analytics/v1/sub2api/accounts/sync-only"]);
 expect(screen.getByText("Channel 1",{selector:"strong"})).toBeVisible();
});

it.each(["filter","next-page"])("updates only channel results for %s, keeping account rows and choices mounted",async change=>{
 let finish!:(response:Response)=>void,pageReads=0,progressReads=0;
 const response={...page(),rates:[0.1,0.2],platforms:["openai","anthropic"]};
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>{
  const u=new URL(input);
  if(u.pathname.endsWith("/accounts/progress")){progressReads++;return idleProgress();}
  if(u.pathname.endsWith("/channel-management/query")){
   pageReads++;if(pageReads>1)return new Promise<Response>(resolve=>{finish=resolve;});
   return Response.json(response);
  }
  return Response.json({data:[]});
 }));
 mount("filters-stable");const user=userEvent.setup();
 await screen.findByText("Channel 1",{selector:"strong"});
 const accountRow=screen.getByRole("listitem",{name:"Owned site"});
 const options=screen.getByLabelText("倍率上限筛选").innerHTML;
 const table=screen.getByRole("table");
 const firstRow=screen.getByText("Channel 1",{selector:"strong"});
 if(change === "filter") await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-5.5");
 else await user.click(screen.getByRole("button",{name:"下一页"}));
 await waitFor(()=>expect(pageReads).toBe(2));
 expect(screen.getByRole("listitem",{name:"Owned site"})).toBe(accountRow);
 expect(screen.queryByRole("status",{name:"加载站点账号"})).not.toBeInTheDocument();
 expect(screen.getByLabelText("倍率上限筛选").innerHTML).toBe(options);
 expect(screen.getByLabelText("检测模型筛选")).toHaveValue(change === "filter" ? "gpt-5.5" : "");
 expect(screen.getByText("Channel 1",{selector:"strong"})).toBe(firstRow);
 expect(firstRow).toBeVisible();
 expect(screen.getByRole("table")).toBe(table);
 expect(table.parentElement).toHaveAttribute("inert");
 expect(table.parentElement).toHaveAttribute("aria-busy","true");
 expect(screen.getByText("正在更新渠道结果，暂显示更新前内容…")).toBeVisible();
 expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
 expect(screen.getByRole("button",{name:"下一页"})).toBeDisabled();
 expect(progressReads).toBe(1);
 await act(async()=>{finish(Response.json({...response,...page(change === "next-page" ? 25 : 0),read_at_ms:2}));});
 await screen.findByText(change === "next-page" ? "Channel 26" : "Channel 1",{selector:"strong"});
 expect(screen.getByRole("listitem",{name:"Owned site"})).toBe(accountRow);
 expect(screen.getByRole("table")).toBe(table);
 expect(table.parentElement).not.toHaveAttribute("inert");
 expect(progressReads).toBe(1);
});

it.each([
 ["POST","/v1/sub2api/channels"],
 ["POST","/v1/channel-management"],
 ["PUT","/v1/channel-management/fixture"],
 ["DELETE","/v1/channel-management/fixture"],
 ["POST","/v1/sub2api/check-batch"],
 ["POST","/v1/model-fingerprints/check"],
 ["POST","/v1/channel-routes/optimize"],
])("keeps the mounted page after accepted %s %s and blocks unverified row actions",async(method,path)=>{
 let finish!:(response:Response)=>void,reads=0;
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input);
  if(init?.method)return Response.json({ok:true});
  if(url.pathname.endsWith("/accounts/progress"))return idleProgress();
  if(url.pathname.endsWith("/channel-management/query")){
   if(++reads>2){expect(url.searchParams.get("cursor")).toBe("25");expect(url.searchParams.get("refresh_cursor")).toBe("1");return new Promise<Response>(resolve=>{finish=resolve;});}
   return Response.json(page(Number(url.searchParams.get("cursor")||0)));
  }
  return Response.json({data:[]});
 }));
 mount(`write-stable-${method}-${path}`);
 await screen.findByText("Channel 1",{selector:"strong"});
 await userEvent.setup().click(screen.getByRole("button",{name:"下一页"}));
 await screen.findByText("Channel 26",{selector:"strong"});
 const table=screen.getByRole("table"),accountRow=screen.getByRole("listitem",{name:"Owned site"});
 const row=screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"});
 await act(async()=>{await controlRequest(path,{method});});
 await waitFor(()=>expect(reads).toBe(3));
 expect(screen.getByRole("listitem",{name:"Owned site"})).toBe(accountRow);
 expect(document.querySelector(".sub-results-table table")).toBe(table);
 expect(table.parentElement).toHaveAttribute("inert");
 expect(row).toBeVisible();
 expect(within(row).getByRole("button",{name:"检测 Owned site Channel 26",hidden:true})).toBeDisabled();
 expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
 expect(screen.getByText("正在更新渠道结果，暂显示更新前内容…")).toBeVisible();
 await act(async()=>{finish(Response.json({...page(25),revision:"updated",read_at_ms:2}));});
 await waitFor(()=>expect(table.parentElement).not.toHaveAttribute("inert"));
 expect(screen.getByRole("table")).toBe(table);
 expect(screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"})).toBe(row);
});

it("submits the import form on page two and reconciles that same page after the write",async()=>{
 const probe={status:"success",text:"fixture",ttft_ms:1,duration_ms:2,protocol:"responses" as const};
 const result={model:"gpt-5.5",checked_at:1,availability:probe,quality:probe,verdict:"not_applicable"};
 const cursors:string[]=[];let posts=0,bindingReads=0,finish!:(response:Response)=>void;
 const readyPage=(offset:number)=>{const p=page(offset);return {...p,rows:p.rows.map(r=>({...r,target:{...r.target,models:[{model:"gpt-5.5",state:"done",message:"",result}]},checks:[{model:"gpt-5.5",state:"done",message:"",result}]}))};};
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input),path=url.pathname;
  if(path.endsWith("/sub2api/channels")&&init?.method==="POST"){posts++;return Response.json({message:"已添加"});}
  if(path.endsWith("/sub2api/channels")){bindingReads++;expect(Object.fromEntries(url.searchParams)).toEqual({view:"bindings",account_id:"owned",group_id:"26"});return Response.json({data:[],labels:{},unavailable_sources:[]});}
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(path.endsWith("/channel-management/query")){
   const cursor=url.searchParams.get("cursor")||"";cursors.push(cursor);
   if(posts){expect(cursor).toBe("25");expect(url.searchParams.get("refresh_cursor")).toBe("1");return new Promise<Response>(resolve=>{finish=resolve;});}
   return Response.json(readyPage(Number(cursor||0)));
  }
  if(path.endsWith("/sources"))return Response.json({data:[{id:"fixture",name:"Fixture"}]});
  if(path.endsWith("/import-status"))return Response.json({state:"idle",job_kind:"",can_import:true});
  if(path.endsWith("/access"))return Response.json({available:true,status:"ok"});
  if(path.endsWith("/channel-options"))return Response.json({supported:true,revision:"r1",keys:[{key_id:"fixture-key",position:1,prefix:"fixture"}],channels:[]});
  return Response.json({data:[],labels:{},unavailable_sources:[]});
 }));
 mount("import-second-page");const user=userEvent.setup();
 await screen.findByText("Channel 1",{selector:"strong"});
 await user.click(screen.getByRole("button",{name:"下一页"}));
 const row=await screen.findByRole("row",{name:"查看 Owned site Channel 26 渠道详情"});
 const table=screen.getByRole("table");
 await user.click(within(row).getByRole("button",{name:"添加到渠道"}));
 const d=within(screen.getByRole("dialog"));
 expect(cursors).toEqual(["","25"]);
 expect(bindingReads).toBe(1);
 await user.selectOptions(d.getByLabelText("添加到 uni-api 来源"),"fixture");
 await waitFor(()=>expect(d.getByLabelText("添加到 API key")).toBeEnabled());
 await user.selectOptions(d.getByLabelText("添加到 API key"),"fixture-key");
 await waitFor(()=>expect(d.getByRole("button",{name:"添加到渠道"})).toBeEnabled());
 await user.click(d.getByRole("button",{name:"添加到渠道"}));
 await waitFor(()=>expect(cursors).toEqual(["","25","25"]));
 expect(posts).toBe(1);expect(document.querySelector(".sub-results-table table")).toBe(table);
 expect(document.querySelector(".sub-pagination")).toHaveTextContent("2 / 3");
 await act(async()=>finish(Response.json({...readyPage(25),revision:"after-import",read_at_ms:3})));
 await waitFor(()=>expect(table.parentElement).not.toHaveAttribute("inert"));
 expect(screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"})).toBe(row);
 expect(document.querySelector(".sub-pagination")).toHaveTextContent("2 / 3");
});
