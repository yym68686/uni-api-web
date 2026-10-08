import { afterEach, expect, it, vi } from "vitest";
import { readAccountSession } from "./accountSession";
afterEach(()=>{delete globalThis.__uniConsoleSessionBootstrap;vi.unstubAllGlobals();});
it('awaits the early authentication result and only consumes it once',async()=>{
 let finish!:(value:unknown)=>void;
 globalThis.__uniConsoleSessionBootstrap={startedAt:Date.now(),result:new Promise(resolve=>{finish=resolve;})};
 const fetcher=vi.fn(async()=>Response.json({enabled:true,authenticated:false,username:''}));vi.stubGlobal('fetch',fetcher);
 let settled=false;const request=readAccountSession().then(value=>{settled=true;return value;});
 await Promise.resolve();expect(settled).toBe(false);expect(fetcher).not.toHaveBeenCalled();
 const session={enabled:true,authenticated:true,username:'alice'};finish(session);expect(await request).toEqual(session);
 expect(await readAccountSession()).toEqual({enabled:true,authenticated:false,username:''});expect(fetcher).toHaveBeenCalledTimes(1);
});
it.each(['expired','failed','invalid'])('falls back to a live read for %s bootstrap',async kind=>{
 const current={enabled:true,authenticated:true,username:'current'};
 vi.stubGlobal('fetch',vi.fn(async()=>Response.json(current)));
 globalThis.__uniConsoleSessionBootstrap={startedAt:Date.now()-(kind==='expired'?11000:0),result:kind==='failed'?Promise.reject(Error('offline')):Promise.resolve(kind==='invalid'?{authenticated:true}:{enabled:true,authenticated:true,username:'old'})};
 expect(await readAccountSession()).toEqual(current);
});
