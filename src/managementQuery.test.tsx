import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, focusManager, onlineManager } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { ManagementRow } from "./channelManagement";
import { subFilterDefaults } from "./sub2apiPreferences";
import {
  managementOptimizationSnapshot,
  readManagementPage,
  readManagementSelection,
  useManagementPage,
} from "./managementQuery";
import type { ManagementPage, ManagementSelection } from "./managementQuery";
import { controlRequest } from "./api";

afterEach(() => {
  cleanup();
  delete globalThis.__uniManagementBootstrap;
  vi.unstubAllGlobals();
});
const row = (id: string) =>
  ({
    id,
    account: { id: "owned", targets: [] },
    target: { group_id: Number(id) },
    checks: [],
    accountIds: ["owned"],
  }) as unknown as ManagementRow;
const page = (revision = "new", total = 61): ManagementPage => ({
  format: "management-page-v1",
  revision,
  read_at_ms: 123,
  rows: Array.from({ length: Math.min(25, total) }, (_, i) => row(String(i))),
  total,
  eligible_count: total,
  offset: 0,
  page_size: 25,
  next_cursor: total > 25 ? "next" : undefined,
  selection_token: "signed-selection",
  context: {
    prices: [],
    imports: [],
    routes: {},
    fingerprints: Object.fromEntries(
      Array.from({ length: Math.min(25, total) }, (_, i) => [String(i), []]),
    ),
    fingerprint_models: [],
  },
  accounts: [],
  rates: [0, 1],
  platforms: ["openai"],
  models: ["gpt-5.5"],
  detection_models: ["gpt-5.5"],
});
const selection = (): ManagementSelection => ({
  format: "management-selection-v1",
  revision: "new",
  read_at_ms: 124,
  total: 61,
  eligible_count: 61,
  rows: Array.from({ length: 61 }, (_, i) => row(String(i))),
  eligible: Array.from({ length: 61 }, (_, i) => row(String(i))),
  input: {
    accounts: [],
    channels: [],
    checks: [],
    imports: [],
    routes: {},
    unavailable_sources: [],
    unavailable_imports: [],
  },
  prices: [],
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

it("correlates the painted page with its exact request without logging private inputs", async () => {
  const frames = new Map<number, FrameRequestCallback>();
  let nextFrame = 0, requestId = "";
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
    frames.set(++nextFrame, callback);
    return nextFrame;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
  const output = vi.spyOn(console, "info").mockImplementation(() => {});
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    requestId = new Headers(init.headers).get("X-Console-Request-ID")!;
    return Response.json({ ...page(), owner: "trace-private-owner" });
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook = renderHook(() => useManagementPage("trace-private-owner", { ...subFilterDefaults, model: "private-filter" }), { wrapper });
  try {
    await waitFor(() => expect(hook.result.current.data).toBeDefined());
    for (let frame = 0; frame < 2; frame++) {
      act(() => {
        for (const [id, callback] of [...frames]) {
          frames.delete(id);
          callback(performance.now());
        }
      });
    }
    const detail = output.mock.calls.find(([event]) => event === "management_page_current")?.[1];
    expect(JSON.parse(detail)).toMatchObject({
      request_id: requestId,
      read_trigger: "mount",
      read_trigger_scope: "client_declared_not_authoritative",
      current_validation_acknowledged: false,
      response_headers_ms: expect.any(Number),
      body_and_json_ms: expect.any(Number),
      wire_expand_and_validate_ms: expect.any(Number),
      request_to_paint_ms: expect.any(Number),
      decoded_to_paint_ms: expect.any(Number),
    });
    expect(detail).not.toMatch(/private-filter|trace-private-owner|analytics\/|rows":\[/);
    expect(fetch).toHaveBeenCalledTimes(1);
  } finally {
    hook.unmount();
    client.clear();
  }
});

it("invalidates the visible current page only after an accepted control write", async () => {
  let reads = 0,
    fail = true;
  const triggers: (string | null)[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method)
        return Response.json(fail ? { error: "rejected" } : { ok: true }, {
          status: fail ? 409 : 200,
        });
      reads++;
      triggers.push(new Headers(init?.headers).get("X-Console-Read-Trigger"));
      return Response.json(page());
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const hook = renderHook(() => useManagementPage("owner", subFilterDefaults), {
    wrapper,
  });
  await waitFor(() => expect(hook.result.current.data).toBeDefined());
  await expect(
    controlRequest("/v1/fixture", { method: "POST" }),
  ).rejects.toThrow();
  expect(reads).toBe(1);
  fail = false;
  await act(async () => {
    await controlRequest("/v1/fixture", { method: "POST" });
  });
  await waitFor(() => expect(reads).toBe(2));
  expect(triggers).toEqual(["mount", "accepted_write"]);
  hook.unmount();
  client.clear();
});

it("tags independent filter, page and refetch reads without changing query keys, paths or read counts", async () => {
  const calls: { url: URL; init: RequestInit; trigger: string | null }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init: RequestInit) => {
    calls.push({ url: new URL(input), init, trigger: new Headers(init.headers).get("X-Console-Read-Trigger") });
    return Response.json(page());
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const filters = { ...subFilterDefaults, search: "private-search" };
  const filtered = { ...filters, model: "private-model" };
  const hook = renderHook(({ owner, filters, cursor }) => useManagementPage(owner, filters, cursor), {
    wrapper, initialProps: { owner: "private-owner", filters, cursor: "" },
  });
  try {
    await waitFor(() => expect(hook.result.current.data).toBeDefined());
    hook.rerender({ owner: "private-owner", filters: filtered, cursor: "" });
    await waitFor(() => { expect(calls).toHaveLength(2); expect(hook.result.current.data).toBeDefined(); });
    hook.rerender({ owner: "private-owner", filters: filtered, cursor: "private-cursor" });
    await waitFor(() => { expect(calls).toHaveLength(3); expect(hook.result.current.data).toBeDefined(); });
    await act(async () => { await hook.result.current.refetch(); });
    await waitFor(() => expect(calls).toHaveLength(4));
    expect(client.getQueryCache().getAll().map(query => query.queryKey)).toEqual([["management-query", "private-owner", filtered, "private-cursor"]]);
    hook.rerender({ owner: "other-private-owner", filters: filtered, cursor: "private-cursor" });
    await waitFor(() => { expect(calls).toHaveLength(5); expect(hook.result.current.data).toBeDefined(); });
    expect(calls.map(call => call.trigger)).toEqual(["mount", "filter_change", "page_change", "refetch", "mount"]);
    for (const [i, call] of calls.entries()) {
      expect(call.url.pathname).toBe("/analytics/v1/channel-management/query");
      expect([...call.url.searchParams.keys()].sort()).toEqual(i === 0 ? ["filters"] : i === 1 ? ["filters", "model_view", "receipt_view"] : ["cursor", "filters", "model_view", "receipt_view", "refresh_cursor"]);
      expect(JSON.parse(call.url.searchParams.get("filters")!)).toEqual(i === 0 ? filters : filtered);
      expect(call.init.method).toBeUndefined();
      expect(call.init.cache).toBe("no-cache");
      expect(call.init.signal?.aborted).toBe(false);
    }
  } finally { hook.unmount(); client.clear(); }
});

it("negotiates selected receipts only for model pages and safely falls back during rolling deployment", async () => {
  const calls: URL[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input); calls.push(url);
    return url.searchParams.has("receipt_view")
      ? new Response("筛选参数无效", {status:400})
      : Response.json(page());
  }));
  await readManagementPage({...subFilterDefaults, model:"gpt-5.5"}, "", new AbortController().signal);
  expect(calls.map(url => url.searchParams.get("receipt_view"))).toEqual(["selected", null]);
  expect(calls.map(url => url.searchParams.get("model_view"))).toEqual(["selected", null]);
  expect(calls[1].searchParams.get("filters")).toBe(calls[0].searchParams.get("filters"));
  const wrong = page(); wrong.rows[0].target.receipt_model = "other";
  vi.stubGlobal("fetch", vi.fn(async () => Response.json(wrong)));
  await expect(readManagementPage({...subFilterDefaults, model:"gpt-5.5"}, "", new AbortController().signal)).rejects.toThrow("回执模型范围不一致");
});

it("rejects incomplete or conflicting single-model summary attestations", async () => {
  for (const [model, receipt] of [["other", "gpt-5.5"], ["gpt-5.5", undefined]]) {
    const wrong = page();
    wrong.rows[0].target.summary_model = model;
    wrong.rows[0].target.receipt_model = receipt;
    vi.stubGlobal("fetch", vi.fn(async () => Response.json(wrong)));
    await expect(readManagementPage({...subFilterDefaults, model:"gpt-5.5"}, "", new AbortController().signal)).rejects.toThrow("摘要模型范围不一致");
  }
});

it.each(["sync-selected","sync-only"])("keeps the current page after %s while ordinary configuration writes still refresh", async endpoint => {
  let reads=0;
  vi.stubGlobal("fetch",vi.fn(async(_input:string,init?:RequestInit)=>{
    if(init?.method)return Response.json({queued:1},{status:202});
    reads++;return Response.json(page());
  }));
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook=renderHook(()=>useManagementPage("owner",subFilterDefaults),{wrapper});
  await waitFor(()=>expect(hook.result.current.data?.rows).toHaveLength(25));
  const original=hook.result.current.data;
  await act(async()=>{await controlRequest(`/v1/sub2api/accounts/${endpoint}`,{method:"POST",body:JSON.stringify({models:["gpt-5.5"]})});});
  expect(reads).toBe(1);expect(hook.result.current.data).toBe(original);
  expect(hook.result.current.isFetching).toBe(false);
  await act(async()=>{await controlRequest("/v1/prices",{method:"PUT",body:"{}"});});
  await waitFor(()=>expect(reads).toBe(2));
  hook.unmount();client.clear();
});

it("reads one current page and separately verifies the complete 61-row selection without executing an action", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit) => {
      const url = new URL(input);
      calls.push(url.pathname);
      expect(url.pathname).toBe("/analytics/v1/channel-management/query");
      expect(init.method).toBeUndefined();
      expect(init.cache).toBe(url.searchParams.has("selection") ? "no-store" : "no-cache");
      expect(JSON.parse(url.searchParams.get("filters")!)).toEqual(
        subFilterDefaults,
      );
      return Response.json(
        url.searchParams.has("selection") ? selection() : page(),
      );
    }),
  );
  const signal = new AbortController().signal;
  const first = await readManagementPage(subFilterDefaults, "", signal);
  expect(first.rows).toHaveLength(25);
  expect(first.eligible_count).toBe(61);
  const all = await readManagementSelection(subFilterDefaults, first, signal);
  expect(all.rows).toHaveLength(61);
  expect(all.eligible).toHaveLength(61);
  expect(
    managementOptimizationSnapshot(all, subFilterDefaults).rows,
  ).toHaveLength(61);
  expect(calls).toHaveLength(2);
});

it("keeps signed page position during mixed releases and only falls back for the old parameter parser",async()=>{
 const calls:URL[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>{
  const url=new URL(input);calls.push(url);
  if(url.searchParams.has("refresh_cursor"))return new Response("筛选参数无效\n",{status:400});
  return Response.json(page());
 }));
 await readManagementPage(subFilterDefaults,"signed-position",new AbortController().signal);
 expect(calls).toHaveLength(2);
 expect(calls.map(u=>u.searchParams.get("cursor"))).toEqual(["signed-position","signed-position"]);
 expect(calls.map(u=>u.searchParams.get("refresh_cursor"))).toEqual(["1",null]);
 for(const status of [400,401,403,409,503]) {
  const fetcher=vi.fn(async()=>new Response("permission/revision/read failed",{status}));vi.stubGlobal("fetch",fetcher);
  await expect(readManagementPage(subFilterDefaults,"signed-position",new AbortController().signal)).rejects.toThrow();
  expect(fetcher).toHaveBeenCalledTimes(1);
 }
});

it("rejects a truncated selection, a revision mismatch and duplicate rows", async () => {
  for (const mutate of [
    (s: ManagementSelection) => {
      s.rows = s.rows.slice(0, 25);
    },
    (s: ManagementSelection) => {
      s.revision = "other";
    },
    (s: ManagementSelection) => {
      s.eligible[60] = row("foreign");
    },
    (s: ManagementSelection) => {
      s.rows[60] = s.rows[0];
    },
  ]) {
    const invalid = selection();
    mutate(invalid);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json(invalid)),
    );
    await expect(
      readManagementSelection(
        subFilterDefaults,
        page(),
        new AbortController().signal,
      ),
    ).rejects.toThrow();
  }
});

it("rejects incomplete pages instead of reporting a successful empty result", async () => {
  for (const invalid of [
    { ...page(), rows: [] },
    { ...page(), format: "summary-v1" },
    { ...page(), total: 1 },
    { ...page(), next_cursor: undefined },
    { ...page(), eligible_count: 62 },
  ]) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json(invalid)),
    );
    await expect(
      readManagementPage(subFilterDefaults, "", new AbortController().signal),
    ).rejects.toThrow("分页数据不完整");
  }
});

it("hides preloaded data, cancels superseded filters and does not display old rows on refresh failure", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(
    ["management-query", "owner", subFilterDefaults, ""],
    page("cached"),
  );
  const requests: {
    pending: ReturnType<typeof deferred<Response>>;
    signal: AbortSignal;
    filters: unknown;
  }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string, init: RequestInit) => {
      const pending = deferred<Response>();
      requests.push({
        pending,
        signal: init.signal as AbortSignal,
        filters: JSON.parse(new URL(input).searchParams.get("filters")!),
      });
      return pending.promise;
    }),
  );
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const hook = renderHook(
    ({ filters }) => useManagementPage("owner", filters),
    { wrapper, initialProps: { filters: { ...subFilterDefaults } } },
  );
  expect(hook.result.current.data).toBeUndefined();
  expect(hook.result.current.displayPage).toBeUndefined();
  await waitFor(() => expect(requests).toHaveLength(1));
  hook.rerender({ filters: { ...subFilterDefaults, model: "gpt-5.5" } });
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[0].signal.aborted).toBe(true);
  expect(hook.result.current.data).toBeUndefined();
  await act(async () => {
    requests[1].pending.resolve(Response.json(page("current")));
  });
  await waitFor(() =>
    expect(hook.result.current.data?.revision).toBe("current"),
  );
  await act(async () => {
    requests[0].pending.resolve(Response.json(page("obsolete")));
  });
  expect(hook.result.current.data?.revision).toBe("current");
  let refresh!: ReturnType<typeof hook.result.current.refetch>;
  act(() => {
    refresh = hook.result.current.refetch();
  });
  await waitFor(() => expect(requests).toHaveLength(3));
  expect(hook.result.current.data).toBeUndefined();
  await act(async () => {
    requests[2].pending.resolve(new Response("读取失败", { status: 503 }));
    await refresh;
  });
  await waitFor(() => expect(hook.result.current.isError).toBe(true));
  expect(hook.result.current.data).toBeUndefined();
  hook.unmount();
  client.clear();
});

it("does not reuse a mounted page's layout data across account changes",async()=>{
 let finish!:(response:Response)=>void;
 vi.stubGlobal("fetch",vi.fn(async()=>new Promise<Response>(resolve=>{finish=resolve;})));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
 const hook=renderHook(({owner})=>useManagementPage(owner,subFilterDefaults),{wrapper,initialProps:{owner:"first"}});
 await act(async()=>{finish(Response.json({...page(),owner:"first"}));});
 await waitFor(()=>expect(hook.result.current.displayPage?.owner).toBe("first"));
 hook.rerender({owner:"second"});
 expect(hook.result.current.data).toBeUndefined();
 expect(hook.result.current.displayPage).toBeUndefined();
 await act(async()=>{finish(Response.json({...page(),owner:"second"}));});
 await waitFor(()=>expect(hook.result.current.displayPage?.owner).toBe("second"));
 hook.unmount();client.clear();
});


it.each(["owner", "other", undefined])("only consumes a parallel initial page with an attested matching owner (%s)", async responseOwner => {
  const initial={...page(), owner:responseOwner};
  const fresh={...page("fresh"), owner:"owner"};
  globalThis.__uniManagementBootstrap={owner:"owner",filters:JSON.stringify(subFilterDefaults),startedAt:Date.now(),controller:new AbortController(),result:Promise.resolve(initial)};
  vi.stubGlobal("fetch",vi.fn(async()=>Response.json(fresh)));
  const result=await readManagementPage(subFilterDefaults,"",new AbortController().signal,"owner");
  expect(result.revision).toBe(responseOwner==="owner"?"new":"fresh");
  expect(fetch).toHaveBeenCalledTimes(responseOwner==="owner"?0:1);
});

it("rejects a response whose session owner changed during a normal read", async()=>{
  vi.stubGlobal("fetch",vi.fn(async()=>Response.json({...page(),owner:"other"})));
  await expect(readManagementPage(subFilterDefaults,"",new AbortController().signal,"owner")).rejects.toThrow("登录账号已变化");
});


it.each(["idle","running","queued"])("does not clear or reread management on focus, reconnect or timers (%s)", async state=>{
  const live={...page(),accounts:[{id:"a",name:"Fixture",base:"https://fixture.test",email:"fixture@test",message:"",synced_at:0,targets:[],state}]} as ManagementPage;
  const fetcher=vi.fn(async()=>Response.json(live));vi.stubGlobal("fetch",fetcher);
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  const wrapper=({children}:{children:ReactNode})=><QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook=renderHook(()=>useManagementPage("owner",subFilterDefaults),{wrapper});
  await waitFor(()=>expect(hook.result.current.data?.rows).toHaveLength(25));
  expect(fetcher).toHaveBeenCalledTimes(1);
  vi.useFakeTimers();
  try {
    await act(async()=>{focusManager.setFocused(false);focusManager.setFocused(true);onlineManager.setOnline(false);onlineManager.setOnline(true);await vi.advanceTimersByTimeAsync(120_000);});
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(hook.result.current.data?.rows).toHaveLength(25);
    await act(async()=>{await hook.result.current.refetch();});
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(hook.result.current.data?.rows).toHaveLength(25);
  } finally {hook.unmount();client.clear();vi.useRealTimers();focusManager.setFocused(undefined);onlineManager.setOnline(true);}
});
