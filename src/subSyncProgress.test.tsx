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
