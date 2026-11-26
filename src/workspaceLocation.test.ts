import { expect, it } from "vitest";
import { defaultFilters } from "./preferences";
import { readWorkspace, requestLocation, workspaceHash } from "./workspaceLocation";

it("round trips shareable workspace context without free-text diagnostic input or credentials", () => {
  const filters = {...defaultFilters, sourceId:"source-a", keyId:"source-a::opaque", model:"model-a", provider:"channel-a",
    balanceThreshold:"3.25", search:"private free text", failureReason:"upstream_http_429"};
  const hash = workspaceHash("channels",filters,"channel-model-id");
  expect(hash).not.toContain("private");
  expect(readWorkspace(hash,true)).toMatchObject({view:"channels",detail:"channel-model-id",
    filters:{sourceId:"source-a",keyId:"source-a::opaque",model:"model-a",provider:"channel-a",search:"",balanceThreshold:"3.25",failureReason:"upstream_http_429"}});
  expect(workspaceHash("overview", filters)).not.toContain("provider");
  expect(readWorkspace("#view=sources", false).view).toBeUndefined();
});

it("preserves the exact log scope and rejects malformed or future absolute windows", () => {
  const to = Date.now() - 1000;
  const window = {from:to - 23_897,to};
  const selected = {id:"request-1",source:"source-a",instance:"instance-3"};
  const hash = requestLocation({...defaultFilters,sort:"wait-desc",search:"hidden"},selected,window);
  expect(readWorkspace(hash,true)).toMatchObject({view:"requests",request:selected,logWindow:window,filters:{sort:"wait-desc",search:""}});
  expect(readWorkspace("#view=requests&from=1&to=2.5",true).logWindow).toBeNull();
  expect(readWorkspace(requestLocation(defaultFilters,null,{...window,to:Date.now()+600_000}),true).logWindow).toBeNull();
});
