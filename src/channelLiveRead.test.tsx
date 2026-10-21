import {expect, it, vi} from "vitest";
import {render, screen, waitFor} from "@testing-library/react";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import {Sub2apiChecks} from "./Sub2apiChecks";
import {loadSummaryCache} from "./summaryCache";

vi.mock("./summaryCache", () => ({loadSummaryCache: vi.fn(), saveSummaryCache: vi.fn()}));

it("never publishes persisted channel snapshots while live reads remain pending", async () => {
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
  await waitFor(() => expect(client.getQueryState(["sub2api"])?.fetchStatus).toBe("fetching"));
  expect(loadSummaryCache).not.toHaveBeenCalled();
  expect(screen.queryByText("cached-group", {selector:"strong"})).not.toBeInTheDocument();
  expect(client.getQueryData(["sub2api"])).toBeUndefined();
  expect(client.getQueryData(["channel-management"])).toBeUndefined();
});
