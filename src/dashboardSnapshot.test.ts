import { expect, it } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { saveDashboardSnapshot, restoreDashboardSnapshot, restoredDashboardAt, clearDashboardSnapshots } from "./dashboardSnapshot";
const metricsKey = ["metrics", "alice:all", "primary::key-one", "24h", "all", "all", true, "gpt-6-astra"];
function fixture() {
  const client = new QueryClient();
  const entries: [unknown[], unknown][] = [
    [metricsKey, { data: [{ model: "gpt-6-astra", stats: { success: 42 } }], total: { requests: 42 }, channel_spend: [] }],
    [["keys", "alice:all"], { data: [{key_id:"primary::key-one",prefix:"masked"}], can_inspect_all:true }],
    [["catalog", "alice:all", "primary::key-one", "all", "all"], { data: [{provider:"one",model:"gpt-6-astra"}] }],
    [["balance", "alice:all", null, "one"], { status:"complete",keys:[{amount:20}] }],
    [["channel-checks","alice"], {data:[{text:"fixture",curl_token:"sealed-secret"}]}],
    [["channel-settings","primary"], {api_key:"secret-settings-key"}],
  ];
  const subscriptions = entries.map(([queryKey, data]) => {
    client.setQueryData(queryKey, data, {updatedAt:Date.now()-500});
    return new QueryObserver(client,{queryKey,queryFn:async()=>data,staleTime:Infinity}).subscribe(()=>{});
  });
  return {client,done:()=>{subscriptions.forEach(fn=>fn());client.clear();}};
}
it("restores one complete exact-scope dashboard and revalidates without storing secrets",()=>{
 const {client,done}=fixture();saveDashboardSnapshot(client,"alice",metricsKey);
 const raw=Object.values(localStorage).join("");expect(raw).not.toContain("sealed-secret");expect(raw).not.toContain("secret-settings-key");
 const fresh=new QueryClient();restoreDashboardSnapshot(fresh,"alice");
 expect(fresh.getQueryData(metricsKey)).toEqual(client.getQueryData(metricsKey));
 expect(fresh.getQueryData(["balance","alice:all",null,"one"])).toEqual({status:"complete",keys:[{amount:20}]});
 expect(fresh.getQueryState(metricsKey)?.isInvalidated).toBe(true);
 expect(restoredDashboardAt(fresh,metricsKey)).toBeGreaterThan(0);
 expect(fresh.getQueryData([...metricsKey.slice(0,2),"primary::key-other",...metricsKey.slice(3)])).toBeUndefined();
 expect(fresh.getQueryData([...metricsKey.slice(0,-1),"gemini-3.1-pro"])).toBeUndefined();
 fresh.setQueryData(metricsKey,{live:true});expect(restoredDashboardAt(fresh,metricsKey)).toBeUndefined();done();fresh.clear();
});
it("does not replace a complete snapshot with an unfinished or failed refresh",()=>{
 const {client,done}=fixture();saveDashboardSnapshot(client,"alice",metricsKey);
 const before=Object.values(localStorage).join("");
 client.getQueryCache().find({queryKey:metricsKey})!.setState({fetchStatus:"fetching"});
 saveDashboardSnapshot(client,"alice",metricsKey);expect(Object.values(localStorage).join("")).toBe(before);
 client.getQueryCache().find({queryKey:metricsKey})!.setState({fetchStatus:"idle",status:"error"});
 saveDashboardSnapshot(client,"alice",metricsKey);expect(Object.values(localStorage).join("")).toBe(before);done();
});
it("isolates accounts, expires snapshots, and clears them on logout",()=>{
 const {client,done}=fixture();saveDashboardSnapshot(client,"alice",metricsKey);
 const other=new QueryClient();restoreDashboardSnapshot(other,"bob");expect(other.getQueryData(metricsKey)).toBeUndefined();
 const name=Object.keys(localStorage).find(k=>k.startsWith("uni-console-dashboard:"))!;
 const data=JSON.parse(localStorage.getItem(name)!);data.at=Date.now()-31*60_000;localStorage.setItem(name,JSON.stringify(data));
 restoreDashboardSnapshot(other,"alice");expect(other.getQueryData(metricsKey)).toBeUndefined();
 clearDashboardSnapshots(client);expect(localStorage.getItem(name)).toBeNull();done();other.clear();
});
it("ignores corrupt data and does not replace newer in-memory results",()=>{
 const {client,done}=fixture();saveDashboardSnapshot(client,"alice",metricsKey);
 const fresh=new QueryClient();fresh.setQueryData(metricsKey,{newer:true});restoreDashboardSnapshot(fresh,"alice");expect(fresh.getQueryData(metricsKey)).toEqual({newer:true});
 const name=Object.keys(localStorage).find(k=>k.startsWith("uni-console-dashboard:"))!;localStorage.setItem(name,"not json");expect(()=>restoreDashboardSnapshot(fresh,"alice")).not.toThrow();done();fresh.clear();
});
