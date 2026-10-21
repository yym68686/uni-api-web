import {expect, it, vi} from "vitest";
import {render, screen, waitFor} from "@testing-library/react";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import {Sub2apiChecks} from "./Sub2apiChecks";
import {cachedControlRead} from "./cachedControlRead";
import {loadSummaryCache} from "./summaryCache";

vi.mock("./summaryCache", () => ({loadSummaryCache: vi.fn(), saveSummaryCache: vi.fn()}));

it("restores complete channels with a stale label while every network read remains pending", async () => {
  const account = {id:"a",name:"cached-site",base:"https://fixture.test",email:"",state:"idle",targets:[]};
  const target = {group_id:7,name:"cached-group",platform:"openai",channel:"p",rate:1,key_id:1,active:true,state:"done",result:null,models:[]};
  const now = Date.now();
  vi.mocked(loadSummaryCache).mockImplementation(async (_user, key) => {
    if (key === "account-summary") return {version:"assembled",cachedAt:now,rows:[
      {id:"a",value:{account,position:0}},{id:"a:7",value:{account_id:"a",target,position:0}},
    ]};
    if (key.startsWith("control-read:")) return {version:"read",cachedAt:now,rows:[{id:"data",value:{data:[],unavailable_sources:[],labels:{}}}]};
    return undefined;
  });
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  client.setQueryData(["account"], {authenticated:true,username:"fixture"});
  render(<QueryClientProvider client={client}><Tooltip.Provider><Sub2apiChecks user="fixture"/></Tooltip.Provider></QueryClientProvider>);
  expect(await screen.findByText("cached-group", {selector:"strong"})).toBeVisible();
  expect(screen.queryByRole("status", {name:"加载渠道检测"})).not.toBeInTheDocument();
  expect(screen.getByText(/正在显示上次读取的检测结果/)).toHaveTextContent("正在后台更新");
  expect(screen.getByRole("heading", {name:"渠道管理"}).parentElement).toHaveTextContent("1");
  expect(client.getQueryState(["sub2api"])?.fetchStatus).toBe("fetching");
  expect(client.getQueryState(["channel-management"])?.fetchStatus).toBe("fetching");
  expect(screen.getByRole("button", {name:/检测全部模型|检测已选 .* 个模型/})).toBeDisabled();
});

it("does not restore another account's cache or mark it fresh before the live read", async () => {
  const client = new QueryClient();
  client.setQueryData(["account"], {authenticated:true,username:"current-user"});
  vi.mocked(loadSummaryCache).mockResolvedValue({version:"read",cachedAt:12,rows:[{id:"data",value:{data:["old"]}}]});
  let finish!: (value: {data:string[]}) => void;
  const pending = cachedControlRead(client,["channel-management"],new AbortController().signal,
    () => new Promise<{data:string[]}>(resolve => {finish=resolve;}));
  await waitFor(() => expect(client.getQueryData(["channel-management"])).toMatchObject({data:["old"],cached:true}));
  expect(loadSummaryCache).toHaveBeenCalledWith("current-user",'control-read:["channel-management"]');
  finish({data:["current"]});
  expect(await pending).toEqual({data:["current"],cached:false});
});
