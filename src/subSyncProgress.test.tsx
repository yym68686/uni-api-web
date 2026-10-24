import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider, focusManager } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { SubAccount } from "./Sub2apiChecks";
import { useSubSyncProgress } from "./subSyncProgress";

afterEach(()=>{cleanup();vi.useRealTimers();vi.unstubAllGlobals();focusManager.setFocused(undefined);});
it("polls only lightweight progress during a job and stops on completion without a page request",async()=>{
 vi.useFakeTimers();
 focusManager.setFocused(true);
 let reads=0;
 const accounts=[{id:"account",state:"running",targets:[]}] as unknown as SubAccount[];
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>{
  expect(new URL(input).pathname).toBe("/analytics/v1/sub2api/accounts/progress");
  reads++;
  return Response.json({data:[{...accounts[0],state:reads>=3?"idle":"running",progress:{completed:reads,total:3}}],read_at_ms:reads});
 }));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
 const hook=renderHook(()=>useSubSyncProgress("owner",accounts,123),{wrapper});
 await act(async()=>{await vi.advanceTimersByTimeAsync(100);});
 expect(reads).toBe(1);
 await act(async()=>{await vi.advanceTimersByTimeAsync(3100);});
 expect(reads).toBe(2);
 await act(async()=>{focusManager.setFocused(false);focusManager.setFocused(true);await vi.advanceTimersByTimeAsync(100);});
 expect(reads).toBe(2);
 await act(async()=>{await vi.advanceTimersByTimeAsync(3100);});
 expect(reads).toBe(3);
 await act(async()=>{await vi.advanceTimersByTimeAsync(60_000);});
 expect(reads).toBe(3);
 hook.unmount();client.clear();
});

it("starts a fresh progress stream for each accepted sync even when the page still says idle",async()=>{
 vi.useFakeTimers();focusManager.setFocused(true);
 let reads=0;
 const accounts=[{id:"account",state:"idle",targets:[]}] as unknown as SubAccount[];
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>{
  const url=new URL(input);expect(url.pathname).toBe("/analytics/v1/sub2api/accounts/progress");
  expect(JSON.parse(url.searchParams.get("accounts")!)).toEqual(["account"]);
  reads++;return Response.json({data:[{...accounts[0],state:"idle",progress:{total:0,completed:0,queued:0,running:0,interrupted:0,running_models:[],groups:[]}}],read_at_ms:reads});
 }));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
 const hook=renderHook(({submission})=>useSubSyncProgress("owner",accounts,123,submission),{wrapper,initialProps:{submission:0}});
 await act(async()=>{await vi.advanceTimersByTimeAsync(100);});expect(reads).toBe(1);
 hook.rerender({submission:1});
 await act(async()=>{await vi.advanceTimersByTimeAsync(100);});expect(reads).toBe(2);
 await act(async()=>{await vi.advanceTimersByTimeAsync(6000);});expect(reads).toBe(2);
 hook.rerender({submission:2});
 await act(async()=>{await vi.advanceTimersByTimeAsync(100);});expect(reads).toBe(3);
 expect(hook.result.current.data?.read_at_ms).toBe(3);
 hook.unmount();client.clear();
});

it("loads completed accounts once and retains their summaries while another account finishes",async()=>{
 vi.useFakeTimers();focusManager.setFocused(true);
 const complete={total:64,completed:64,queued:0,running:0,interrupted:0,running_models:[],groups:[]};
 const accounts=[{id:"complete",state:"idle",synced_at:1,targets:[]},{id:"busy",state:"running",synced_at:2,targets:[]}] as unknown as SubAccount[];
 let reads=0;
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>{
  const watched=JSON.parse(new URL(input).searchParams.get("accounts")!);
  expect(watched).toEqual(reads===0?["complete","busy"]:["busy"]);
  reads++;return Response.json({data:[{...accounts[0],...(reads===1?{progress:complete,active_groups:2,completed_groups:2}:{})},{...accounts[1],state:reads===1?"running":"idle",progress:{...complete,total:192,completed:reads===1?100:192}}],read_at_ms:reads});
 }));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
 const hook=renderHook(()=>useSubSyncProgress("owner",accounts,123),{wrapper});
 await act(async()=>{await vi.advanceTimersByTimeAsync(100);});
 expect(hook.result.current.data?.data[0].progress?.completed).toBe(64);
 await act(async()=>{await vi.advanceTimersByTimeAsync(3100);});
 expect(hook.result.current.data?.data[0]).toMatchObject({active_groups:2,completed_groups:2,progress:complete});
 expect(hook.result.current.data?.data[1].progress?.completed).toBe(192);
 await act(async()=>{await vi.advanceTimersByTimeAsync(60_000);});expect(reads).toBe(2);
 hook.unmount();client.clear();
});
