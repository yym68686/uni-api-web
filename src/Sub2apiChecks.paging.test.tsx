import { afterEach, expect, it, vi } from "vitest";
import { act, fireEvent, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiChecks } from "./Sub2apiChecks";
import type { Result, SubAccount, SubTarget } from "./Sub2apiChecks";
import type { ManagementPage, ManagementSelection, ManagementTierSelection } from "./managementQuery";
import type { ManagedChannel, ManagementRow } from "./channelManagement";
import { SUB_MODELS, saveSubModels } from "./sub2apiModels";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
import { controlRequest } from "./api";
import { useState } from "react";
import type { ManagementPagePosition } from "./managementQuery";
import { useConfiguredCheckResultRefresh } from "./configuredCheckProgress";
import { managementColumnsStorageKey } from "./managementOverview";

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
async function openSites() {
  const selector = within(screen.getByRole("navigation", {name: "管理视图"}));
  const button = selector.getByRole("button", {name: "站点账号"});
  if (button.getAttribute("aria-current") !== "true") await userEvent.click(button);
}

async function openChannels() {
  await userEvent.click(within(screen.getByRole("navigation", {name: "管理视图"})).getByRole("button", {name: "渠道"}));
}
function mount(user: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider
      client={client}
    >
      <Tooltip.Provider>
        <Sub2apiChecks user={user} />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
}

it.each(["idle", "interrupted", "stopped", "error"])("revalidates page two once when a running site settles as %s, without resubmitting detection", async terminal => {
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  let pageReads = 0, progressReads = 0;
  let settled = false;
  let finishPage!: (response: Response) => void;
  const requests: string[] = [];
  const check = (state: string) => ({model:"gpt-5.5",state,message:"",result:state === "done" ? {
    model:"gpt-5.5",checked_at:4,verdict:"pass",
    availability:{status:"success",model_match:"match",text:"",ttft_ms:1,duration_ms:1},
    quality:{status:"skipped",text:"",ttft_ms:null,duration_ms:0},
  } as Result : null});
  const snapshot = (offset: number, state: string) => ({...page(offset), read_at_ms:state === "queued" ? 1 : 4,
    accounts:[{...account,state:state === "queued" ? "running" : terminal}],
    rows:page(offset).rows.map(row=>({...row,target:{...row.target,state:state === "queued" ? "queued" : "done",models:[check(state)]},checks:[check(state)]})),
  });
  vi.stubGlobal("fetch", vi.fn(async(input: string, init?: RequestInit) => {
    expect(init?.method || "GET").toBe("GET");
    const url = new URL(input);
    if (url.pathname.endsWith("/channel-management/query")) {
      const cursor = url.searchParams.get("cursor") || "";
      requests.push(cursor); pageReads++;
      if (pageReads === 3) return new Promise<Response>(resolve=>{finishPage=resolve;});
      return Response.json(snapshot(Number(cursor), "queued"));
    }
    if (url.pathname.endsWith("/accounts/progress")) {
      progressReads++;
      const done = settled;
      return Response.json({data:[{...account,state:done ? terminal : "running",progress:{total:1,completed:done ? 1 : 0,queued:done ? 0 : 1,running:0,interrupted:0,running_models:[],groups:[]}}],read_at_ms:done ? 3 : 2});
    }
    return Response.json({data:[]});
  }));
  const user = userEvent.setup(); mount(`settled-${terminal}`, client);
  await screen.findByText("Channel 1", {selector:"strong"});
  await waitFor(()=>expect(progressReads).toBeGreaterThan(0));
  await user.click(screen.getByRole("button", {name:"下一页"}));
  await screen.findByText("Channel 26", {selector:"strong"});
  const row = screen.getByRole("row", {name:"查看 Owned site Channel 26 渠道详情"});
  const queued = within(row).getByText("0/1 可用").closest(".tip-target")!;
  fireEvent.focus(queued);
  expect(await screen.findByRole("tooltip")).toHaveTextContent("1 个排队 / 检测中");
  fireEvent.blur(queued);
  settled = true;
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"]});});
  await waitFor(()=>expect(pageReads).toBe(3));
  expect(requests).toEqual(["", "25", "25"]);
  expect(screen.getByRole("row", {name:"查看 Owned site Channel 26 渠道详情"})).toBe(row);
  expect(screen.getByRole("button", {name:/^检测全部模型 ·/})).toBeEnabled();
  expect(within(row).getByRole("button", {name:"检测 Owned site Channel 26"})).toBeDisabled();
  await act(async()=>{finishPage(Response.json(snapshot(25, "done")));});
  await waitFor(()=>expect(within(row).getByText("1/1 可用")).toBeVisible());
  expect(within(row).queryByText("1 个排队 / 检测中")).not.toBeInTheDocument();
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"]});});
  expect(pageReads).toBe(3);
});

it("reconciles queued rows with the first completed progress read even when account headers already say idle", async () => {
  let pageReads = 0;
  vi.stubGlobal("fetch", vi.fn(async(input: string) => {
    const url = new URL(input);
    if (url.pathname.endsWith("/channel-management/query")) {
      pageReads++;
      const value = page(); value.read_at_ms = pageReads === 1 ? 1 : 3;
      value.rows = value.rows.map(row=>({...row,checks:[{model:"gpt-5.5",state:pageReads === 1 ? "queued" : "interrupted",message:"",result:null}]}));
      return Response.json(value);
    }
    if (url.pathname.endsWith("/accounts/progress")) return Response.json({data:[{...account,progress:{total:25,completed:25,queued:0,running:0,interrupted:0,running_models:[],groups:rows.map(row=>({group_id:row.target.group_id,total:1,completed:1,queued:0,running:0,interrupted:0}))}}],read_at_ms:2});
    return Response.json({data:[]});
  }));
  mount("completed-before-first-poll");
  const row = await screen.findByRole("row", {name:"查看 Owned site Channel 1 渠道详情"});
  await waitFor(()=>expect(pageReads).toBe(2));
  const state = within(row).getByText("0/1 可用").closest(".tip-target")!;
  await waitFor(() => {
    fireEvent.focus(state);
    expect(screen.getByRole("tooltip")).toHaveTextContent("1 个中断");
  });
  expect(within(row).queryByText("1 个排队 / 检测中")).not.toBeInTheDocument();
  expect(pageReads).toBe(2);
});

it("only updates the native row on submission and as its models finish", async () => {
  const owner = "native-check-progress";
  const [carbon, argon] = ["gemini-4-carbon", "gemini-4-argon"] as const;
  saveSubModels(owner, [carbon, argon]);
  const member: ManagedChannel = {
    kind: "configured", source_id: "gateway", source_name: "Gateway", provider: "sub2api-copy-banana", name: "bananarouter",
    models: [carbon, argon], engine: "openai", account_ids: [], account_id: "", group_id: 0, api_key_id: "", key_position: 0,
    key_prefix: "", positions: {}, revision: "native", manageable: false, probe_fingerprint: "current-credential",
  };
  const secondMember = { ...member, source_id: "second", provider: "bananarouter" };
  let stage = -1;
  const pageReads: string[] = [];
  let progressReads = 0;
  let submissions = 0;
  const scopedReads: string[] = [];
  const snapshot = () => {
    const checks = SUB_MODELS.map(model => {
      const index = [carbon, argon].indexOf(model as typeof carbon | typeof argon);
      const finished = index >= 0 && index < stage;
      return { model, state: finished ? "done" : (index >= 0 && stage >= 0 ? "queued" : "idle"), message: "", result: finished ? {
        model, checked_at: stage + 1, verdict: index === 0 ? "pass" : "fail",
        availability: {status: index === 0 ? "success" : "error", text: "", ttft_ms: 1, duration_ms: 1, http_status: index === 0 ? 200 : 429},
        quality: {status: "skipped", text: "", ttft_ms: null, duration_ms: 0},
      } as Result : null };
    });
    const base = page(25);
    return { ...base, accounts: [], rows: [
      { ...base.rows[0], account: { ...account, id: "", name: "未归属渠道" }, accountIds: [],
        configured: { ...member, provider: "bananarouter", members: [member, secondMember] },
        target: { ...base.rows[0].target, name: "bananarouter", models: checks }, checks },
      ...base.rows.slice(1),
    ] };
  };
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input);
    if (init?.method === "POST") {
      expect(url.pathname).toBe("/analytics/v1/channel-management/checks");
      expect(JSON.parse(String(init.body)).targets).toEqual([{ source_id: member.source_id, provider: member.provider, models: [carbon, argon] }]);
      submissions++;
      stage = 0;
      return Response.json({queued:2},{status:202});
    }
    expect(init?.method || "GET").toBe("GET");
    if (url.pathname.endsWith("/channel-management/query")) {
      const cursor = url.searchParams.get("cursor") || "";
      pageReads.push(cursor);
      return Response.json(cursor ? snapshot() : page());
    }
    if (url.pathname.endsWith("/channel-management/checks")) {
      const provider = url.searchParams.get("provider");
      scopedReads.push(provider!);
      if (provider === secondMember.provider) {
        expect(url.searchParams.get("source_id")).toBe(secondMember.source_id);
        return Response.json({data:[]});
      }
      expect(provider).toBe(member.provider);
      expect(url.searchParams.get("source_id")).toBe(member.source_id);
      return Response.json({ data: snapshot().rows[0].checks.filter(check => check.state !== "idle").map(check => ({
        ...check, source_id: member.source_id, provider: member.provider, kind: "model", fingerprint: "",
      })) });
    }
    if (url.pathname.endsWith("/checks/progress")) {
      progressReads++;
      expect(JSON.parse(url.searchParams.get("targets")!)).toEqual([
        { source_id: "gateway", provider: "sub2api-copy-banana" }, { source_id: "second", provider: "bananarouter" },
      ]);
      const active = (model: string, fingerprint = "") => ({
        source_id: member.source_id, provider: member.provider, kind: "model", model, fingerprint,
      });
      return Response.json({ data: [
        ...[carbon, argon].slice(stage).map(model => active(model)),
        active("unrelated-model"),
        // The surviving job on an old credential must not hide completion.
        { ...active(argon, "old-credential"), source_id: secondMember.source_id, provider: secondMember.provider },
      ] });
    }
    if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
    return Response.json({ data: [] });
  }));
  const view = mount(owner);
  await screen.findByText("Channel 1", {selector:"strong"});
  vi.useFakeTimers();
  const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
  try {
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await advance(100);
    const row = screen.getByRole("row", { name: "查看 未归属渠道 bananarouter 渠道详情" });
    const button = within(row).getByRole("button", { name: "检测 未归属渠道 bananarouter" });
    const other = screen.getByRole("row", { name: "查看 Owned site Channel 27 渠道详情" });
    const otherButton = within(other).getByRole("button", { name: "检测 Owned site Channel 27" });
    expect(button).toBeEnabled();
    expect(otherButton).toBeEnabled();
    fireEvent.click(button);
    await advance(100);
    await advance(100);
    expect(button).toBeDisabled();
    expect(otherButton).toBeEnabled();
    expect(within(row).getByText(`0/${SUB_MODELS.length} 可用`)).toBeVisible();
    expect(scopedReads).toEqual([member.provider, secondMember.provider]);
    await advance(3100);
    expect(progressReads).toBe(1);
    expect(pageReads).toEqual(["", "25"]);
    stage = 1;
    await advance(3100);
    expect(within(row).getByText(`1/${SUB_MODELS.length} 可用`)).toBeVisible();
    expect(button).toBeDisabled();
    stage = 2;
    await advance(3100);
    expect(button).toBeEnabled();
    expect(within(row).getByText(`1/${SUB_MODELS.length} 可用`)).toBeVisible();
    expect(pageReads).toEqual(["", "25"]);
    expect(screen.getByRole("row", { name: "查看 Owned site Channel 27 渠道详情" })).toBe(other);
    expect(submissions).toBe(1);
    expect(scopedReads).toEqual(Array.from({length:3}, () => [member.provider, secondMember.provider]).flat());
    expect(progressReads).toBe(3);
    await advance(30_000);
    expect(progressReads).toBe(3);
  } finally {
    view.unmount();
    vi.useRealTimers();
  }
});

it("reconciles the current page if a completed native row no longer matches its result filter", async () => {
  const owner = "native-check-filter", model = "gemini-4-carbon";
  const filters = { ...subFilterDefaults, model, availability: "untested" };
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const value = page();
  const pending = { model, state: "queued", message: "", result: null };
  const config: ManagedChannel = {
    kind: "configured", source_id: "gateway", source_name: "Gateway", provider: "native", name: "Native",
    models: [model], engine: "openai", account_ids: [], account_id: "", group_id: 0, api_key_id: "", key_position: 0,
    key_prefix: "", positions: {}, revision: "native", manageable: false,
  };
  value.rows[0] = { ...value.rows[0], account: {...account,id:""}, configured: config, checks: [pending], selected: pending };
  const key = ["management-query", owner, filters, ""];
  client.setQueryData(key, value);
  const snapshot = client.getQueryData<ManagementPage>(key)!;
  const record = {
    ...pending, state: "done", source_id: config.source_id, provider: config.provider, kind: "model", fingerprint: "",
    result: { model, checked_at: 3, verdict: "pass", availability: { status: "success" }, quality: { status: "skipped" } },
  };
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input);
    if (url.pathname.endsWith("/progress")) return Response.json({data:[]});
    expect(url.searchParams.get("provider")).toBe(config.provider);
    return Response.json({format:"summary-v1",version:"v1",base_version:"",data:[{id:"record",value:record}],removed:[],order:["record"]});
  }));
  vi.useFakeTimers();
  const hook = renderHook(() => useConfiguredCheckResultRefresh(owner, filters, snapshot), {
    wrapper: ({children}) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  try {
    await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
    expect(invalidate).toHaveBeenCalledTimes(1);
    const [match, options] = invalidate.mock.calls[0];
    expect(match?.queryKey).toEqual(["management-query", owner]);
    expect(options).toEqual({cancelRefetch:false});
    expect(match?.predicate?.(client.getQueryCache().find({queryKey:key})!)).toBe(true);
    expect(hook.result.current.displayRows(snapshot)[0]).toBe(snapshot.rows[0]);
  } finally {
    hook.unmount();
    client.clear();
    vi.useRealTimers();
  }
});

it("hides unverified results and disables detection when the completion result read fails, without a retry loop", async () => {
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  let pageReads = 0, progressReads = 0;
  vi.stubGlobal("fetch", vi.fn(async(input: string, init?: RequestInit) => {
    expect(init?.method || "GET").toBe("GET");
    const url = new URL(input);
    if (url.pathname.endsWith("/channel-management/query")) {
      if (++pageReads > 1) return Response.json({error:"fixture result read failed"},{status:503});
      return Response.json({...page(),accounts:[{...account,state:"running"}]});
    }
    if (url.pathname.endsWith("/accounts/progress")) {
      return Response.json({data:[{...account,state:++progressReads > 1 ? "idle" : "running",progress:{total:0,completed:0,queued:0,running:0,interrupted:0,running_models:[],groups:[]}}],read_at_ms:progressReads + 1});
    }
    return Response.json({data:[]});
  }));
  mount("failed-completion-read",client);
  await screen.findByText("Channel 1",{selector:"strong"});
  await waitFor(()=>expect(progressReads).toBe(1));
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"]});});
  await screen.findByText(/fixture result read failed/);
  expect(screen.getByText("Channel 1",{selector:"strong"})).not.toBeVisible();
  expect(screen.getByRole("button",{name:/^检测全部模型 ·/})).toBeDisabled();
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"]});});
  expect(pageReads).toBe(2);
});

it("requests available groups first and switches to all groups explicitly", async () => {
  const requestedStatuses: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input);
    if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
    if (url.pathname.endsWith("/channel-management/query")) {
      const { groupStatus } = JSON.parse(url.searchParams.get("filters")!);
      requestedStatuses.push(groupStatus);
      return Response.json(page());
    }
    return Response.json({ data: [] });
  }));
  mount("group-status-default");
  await screen.findByText("Channel 1", { selector: "strong" });
  const user = userEvent.setup();
  await user.click(screen.getByText("筛选", { selector: "summary" }));
  const filter = screen.getByLabelText("分组状态筛选");
  expect(filter).toHaveValue("available");
  expect(requestedStatuses[0]).toBe("available");
  await user.selectOptions(filter, "all");
  await waitFor(() => expect(requestedStatuses).toContain("all"));
});

it.each(["gemini", "gemini-gcp"])("reveals an untested group from the collapsed availability filter when searching %s", async search => {
  const owner = `clear-availability-${search}`;
  saveSubFilters(owner, { ...subFilterDefaults, accountId: account.id, search, availability: "success" });
  const readFilters: ReturnType<typeof loadSubFilters>[] = [];
  const siteRows = ["gemini", "gemini-gcp"].map((name, i) => ({
    ...rows[i], target: { ...rows[i].target, name },
  }));
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    expect(init?.method || "GET").toBe("GET");
    const url = new URL(input);
    if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
    if (!url.pathname.endsWith("/channel-management/query")) return Response.json({ data: [] });
    const filters = JSON.parse(url.searchParams.get("filters")!);
    readFilters.push(filters);
    expect(url.searchParams.has("cursor")).toBe(false);
    const matched = siteRows.filter(row => row.target.name.includes(filters.search) &&
      (!filters.availability || row.target.name === "gemini"));
    return Response.json({ ...page(), rows: matched, total: matched.length, eligible_count: matched.length, next_cursor: undefined });
  }));
  mount(owner);
  if (search === "gemini") await screen.findByText("gemini", { selector: "strong" });
  else await screen.findByText("没有匹配的渠道");
  expect(screen.queryByText("gemini-gcp", { selector: "strong" })).not.toBeInTheDocument();
  const chip = screen.getByRole("button", { name: "取消可用性筛选：至少一个模型可用" });
  expect(chip).toBeVisible();
  await userEvent.click(chip);

  expect(await screen.findByText("gemini-gcp", { selector: "strong" })).toBeVisible();
  const input = screen.getByLabelText("搜索 sub2api 分组");
  expect(input).toHaveValue(search);
  expect(input).toHaveFocus();
  expect(chip).not.toBeInTheDocument();
  const expected = { search, accountId: account.id, groupStatus: "available", availability: "" };
  expect(readFilters[0]).toMatchObject({ ...expected, availability: "success" });
  expect(readFilters.at(-1)).toMatchObject(expected);
  expect(loadSubFilters(owner)).toMatchObject(expected);
});

it("reuses the current page when returning from another view and resets position when filters change", async () => {
  const reads: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async(input:string)=>{
    const url = new URL(input);
    if(url.pathname.endsWith("/accounts/progress")) return idleProgress();
    if(url.pathname.endsWith("/channel-management/query")) {
      reads.push(url.searchParams.get("cursor") || "");
      return Response.json(page(Number(url.searchParams.get("cursor") || 0)));
    }
    return Response.json({data:[]});
  }));
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  function Navigation() {
    const [visible, setVisible] = useState(true);
    const [position, setPosition] = useState<ManagementPagePosition>();
    return <QueryClientProvider client={client}><Tooltip.Provider>
      <button onClick={()=>setVisible(!visible)}>切换日志</button>
      {visible ? <Sub2apiChecks user="navigation-page" initialPosition={position} onPositionChange={setPosition}/> : <p>日志页面</p>}
    </Tooltip.Provider></QueryClientProvider>;
  }
  render(<Navigation/>);
  const user = userEvent.setup();
  await screen.findByText("Channel 1",{selector:"strong"});
  await user.click(screen.getByRole("button",{name:"下一页"}));
  await screen.findByText("Channel 26",{selector:"strong"});
  await user.click(screen.getByRole("button",{name:"下一页"}));
  await screen.findByText("Channel 51",{selector:"strong"});
  await user.click(screen.getByRole("button",{name:"切换日志"}));
  await screen.findByText("日志页面");
  await user.click(screen.getByRole("button",{name:"切换日志"}));
  await screen.findByText("Channel 51",{selector:"strong"});
  expect(reads).toEqual(["","25","50"]);
  expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
  expect(screen.queryByText("Channel 1",{selector:"strong"})).not.toBeInTheDocument();
  await user.type(screen.getByRole("textbox",{name:"排除渠道名"}),"kiro");
  await screen.findByText("Channel 1",{selector:"strong"});
  await user.click(screen.getByRole("button",{name:"切换日志"}));
  await screen.findByText("日志页面");
  await user.click(screen.getByRole("button",{name:"切换日志"}));
  await screen.findByText("Channel 1",{selector:"strong"});
  expect(screen.getByRole("textbox",{name:"排除渠道名"})).toHaveValue("kiro");
  expect(reads.at(-1)).toBe("");
});

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
  await openSites();
 const button=screen.getByRole("button",{name:detect ? "一键同步并检测" : "一键同步"});
 await user.click(button);
 await waitFor(()=>expect(progressReads).toBe(2));
 expect(pageReads).toBe(2);expect(posts).toBe(1);expect(button).toBeDisabled();
 expect(screen.getByText("Channel 26",{selector:"strong"}).closest("tr")).toBe(row);
 expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
 await act(async()=>{finishProgress(Response.json({data:[{...account,state:"running",job_kind:"sync",sync_models:["gpt-5.5"],active_groups:1,completed_groups:0,progress:{total:32,completed:3,queued:27,running:2,interrupted:0,running_models:["gpt-5.5"],groups:[]}}],read_at_ms:2}));});
  await screen.findByText("模型 3/32");
 expect(screen.getByText("Channel 26",{selector:"strong"}).closest("tr")).toBe(row);
 expect(button).toBeDisabled();expect(pageReads).toBe(2);expect(posts).toBe(1);
 await openChannels();
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
      if (url.pathname.endsWith("/detection-settings")) return Response.json({disabled:[]});
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
  expect(screen.getByRole("button", {name: /检测全部模型 · 61 个渠道/})).toBeVisible();
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("Channel 26", { selector: "strong" });
  expect(
    screen.queryByText("Channel 1", { selector: "strong" }),
  ).not.toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 61 个渠道" }),
  );
  const preview = await screen.findByRole("dialog",{name:"核对批量检测范围"});
  expect(within(preview).getByText(/本次计划 61 \/ 61 个渠道/)).toBeVisible();
  expect(writes).toHaveLength(0);
  await user.click(within(preview).getByRole("button",{name:"确认提交检测"}));
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

it.each([
  { siteStatus: 202, nativeStatus: 0 },
  { siteStatus: 502, nativeStatus: 0 },
  { siteStatus: 202, nativeStatus: 202 },
  { siteStatus: 502, nativeStatus: 202 },
])("keeps the table in place for site HTTP $siteStatus and native HTTP $nativeStatus (0 = no native)", async ({ siteStatus, nativeStatus }) => {
  const owner = `selected-model-batch-feedback-${siteStatus}-${nativeStatus}`;
  const model = "gpt-6.2-sol";
  saveSubFilters(owner, { ...subFilterDefaults, model });
  saveSubModels(owner, [model], SUB_MODELS);
  const configured: ManagedChannel = {
    kind: "configured", source_id: "gateway", source_name: "Gateway", provider: "native", name: "Native",
    models: [model], engine: "gpt", service_tier_scope: "a".repeat(64), account_ids: [], account_id: "", group_id: 0, api_key_id: "", key_position: 0,
    key_prefix: "", positions: {}, revision: "native", manageable: false,
  };
  let previewReads = 0;
  const writes: Record<string, unknown> = {};
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input);
    if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
    if (url.pathname.endsWith("/detection-settings")) return Response.json({ disabled: [] });
    if (init?.method === "POST") {
      const native = url.pathname.endsWith("/channel-management/checks");
      expect(url.pathname).toBe(native ? "/analytics/v1/channel-management/checks" : "/analytics/v1/sub2api/checks");
      const branch = native ? "native" : "site";
      expect(writes).not.toHaveProperty(branch);
      writes[branch] = JSON.parse(String(init.body));
      const status = native ? nativeStatus : siteStatus;
      return status === 202 ? Response.json({ queued: native ? 1 : true }, { status })
        : new Response("<html>Bad Gateway</html>", { status });
    }
    if (url.pathname.endsWith("/channel-management/query")) {
      if (!url.searchParams.has("selection")) return Response.json(page());
      expect(url.searchParams.get("check_selection")).toBe("1");
      expect(JSON.parse(url.searchParams.get("filters")!).model).toBe(model);
      if (++previewReads === 1) return new Response("<html>Bad Gateway</html>", { status: 502 });
      return Response.json({
        format: "management-check-selection-v1", revision: "current", read_at_ms: 2, total: 61, eligible_count: 61,
        eligible: rows.map((row, i) => i === 60 ? { ...row, target: { ...row.target, models: [{ model, state: "queued" }] } }
          : i === 59 && nativeStatus ? { ...row, account: { ...account, id: "" }, accountIds: [], configured } : row),
      });
    }
    return Response.json({ data: [] });
  }));
  mount(owner);
  const user = userEvent.setup();
  const detect = await screen.findByRole("button", { name: "检测所选模型 · 61 个渠道" });
  await waitFor(() => expect(detect).toBeEnabled());
  const table = screen.getByRole("region", { name: "渠道管理表，可横向滚动" });
  const toolbar = table.previousElementSibling;
  expect(toolbar).toHaveClass("management-toolbar");
  await user.click(detect);
  const failure = await screen.findByRole("dialog", { name: "批量检测失败" });
  expect(within(failure).getByRole("alert")).toHaveTextContent("检测范围读取失败，请重试。（HTTP 502）");
  expect(screen.queryByRole("dialog", { name: "核对批量检测范围" })).not.toBeInTheDocument();
  expect(writes).toEqual({});
  expect(table.previousElementSibling).toBe(toolbar);
  await user.click(within(failure).getByRole("button", { name: "知道了" }));
  await user.click(detect);
  const preview = await screen.findByRole("dialog", { name: "核对批量检测范围" });
  expect(within(preview).getByText(/本次计划 60 \/ 61 个渠道/)).toBeVisible();
  expect(writes).toEqual({});
  await user.click(within(preview).getByRole("button", { name: "确认提交检测" }));
  if (siteStatus === 202) {
    const notice = await screen.findByText("批量检测已提交");
    expect(notice).toBeVisible();
    const status = notice.closest('[role="status"]');
    expect(status?.parentElement).toBe(document.body);
    expect(status).toHaveTextContent(/^批量检测已提交$/);
    expect(screen.queryByText(/已收到提交回执/)).not.toBeInTheDocument();
  } else {
    const unconfirmed = await screen.findByRole("dialog", { name: "批量检测提交结果未确认" });
    expect(within(unconfirmed).getByText(/未收到检测回执（HTTP 502）/)).toBeVisible();
    expect(within(unconfirmed).getByText(/避免重复提交/)).toBeVisible();
    if (nativeStatus) expect(within(unconfirmed).getByText(/原生渠道.*已收到提交回执，排队 1 项/)).toBeVisible();
    expect(screen.queryByText("批量检测已提交")).not.toBeInTheDocument();
    await user.click(within(unconfirmed).getByRole("button", { name: "知道了" }));
  }
  expect(screen.getByRole("region", { name: "渠道管理表，可横向滚动" })).toBe(table);
  expect(table.previousElementSibling).toBe(toolbar);
  expect(previewReads).toBe(3);
  expect(writes).toEqual({
    site: { targets: rows.slice(0, nativeStatus ? 59 : 60).map(row => ({ account_id: account.id, group_id: row.target.group_id, models: [model] })) },
    ...(nativeStatus ? { native: { kind: "check", full_model_check: true, targets: [{
      source_id: configured.source_id, provider: configured.provider, models: [model], tier_models: [model], expected_scope: "a".repeat(64),
    }] } } : {}),
  });
});

it("updates a completed site row while another group is still running without rereading the management page", async () => {
  const owner = "incremental-site-suite";
  const model = "gpt-6.2-sol";
  saveSubFilters(owner, { ...subFilterDefaults, model });
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  let pageReads = 0, progressReads = 0, finished = false;
  const detailReads: number[] = [];
  const done = { model, state:"done", message:"", result:{
    model, checked_at:3, verdict:"not_applicable",
    availability:{status:"success",text:"",ttft_ms:1,duration_ms:1},
    quality:{status:"not_applicable",text:"",ttft_ms:null,duration_ms:0},
    service_tiers:{fast:{status:"supported",checked_at:Math.floor(Date.now()/1000)}},
  }};
  const queued = {model,state:"queued",message:"",result:null};
  const list = {...page(),total:2,eligible_count:2,next_cursor:undefined, rows:page().rows.slice(0,2).map(row=>({
    ...row, target:{...row.target,state:"queued",models:[queued]},checks:[queued],selected:queued,
  })),accounts:[{...account,state:"running"}]};
  vi.stubGlobal("fetch", vi.fn(async(input:string) => {
    const path = new URL(input).pathname;
    if (path.endsWith("/channel-management/query")) { pageReads++; return Response.json(list); }
    if (path.endsWith("/accounts/progress")) {
      progressReads++;
      return Response.json({data:[{...account,state:"running",progress:{total:2,completed:Number(finished),queued:2-Number(finished),running:0,interrupted:0,running_models:[],groups:[1,2].map(group_id=>{
        const completed = group_id === 1 && finished;
        return {group_id,state:completed?"done":"queued",total:1,completed:Number(completed),queued:Number(!completed),running:0,interrupted:0,tier_version:Number(completed)};
      })}}],read_at_ms:finished?3:2});
    }
    const details = /\/groups\/(\d+)\/details$/.exec(path);
    if (details) {
      const id = Number(details[1]); detailReads.push(id);
      const check = id === 1 && finished ? done : queued;
      return Response.json({data:[{...account,targets:[{...target(id),state:check.state,models:[check]}]}]});
    }
    return Response.json({data:[]});
  }));
  mount(owner,client);
  const row1 = await screen.findByRole("row",{name:"查看 Owned site Channel 1 渠道详情"});
  const row2 = screen.getByRole("row",{name:"查看 Owned site Channel 2 渠道详情"});
  await waitFor(()=>expect(progressReads).toBeGreaterThan(0));
  await waitFor(()=>expect(detailReads.sort()).toEqual([1,2]));
  expect(within(row1).getByText("排队中")).toBeVisible();
  finished = true;
  detailReads.length = 0;
  await act(async()=>{ await client.refetchQueries({queryKey:["sub-sync-progress"]}); });
  expect(await within(row1).findByText("可用",{selector:".check-status"})).toBeVisible();
  expect(within(row2).getByText("排队中")).toBeVisible();
  expect(screen.getByRole("row",{name:"查看 Owned site Channel 1 渠道详情"})).toBe(row1);
  expect(detailReads).toEqual([1]);
  expect(pageReads).toBe(1);
});

it("opens optimization from a fresh full snapshot after results changed, while batches still reject the old page selection", async () => {
  const calls: URL[] = [];
  vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
    expect(init?.method || "GET").toBe("GET");
    const url=new URL(input);calls.push(url);
    if(url.pathname.endsWith("/accounts/progress"))return idleProgress();
    if(url.pathname.endsWith("/channel-management/query"))return url.searchParams.has("selection")
      ? new Response("渠道配置或检测结果已变化，请重新读取",{status:409}) : Response.json(page());
    if(url.pathname.endsWith("/optimization-preview"))return Response.json({
      ...selection(),format:"management-optimization-preview-v1",revision:"updated-results",owner:"fresh-optimization",
      filters:JSON.parse(url.searchParams.get("filters")!),total:60,eligible_count:60,rows:rows.slice(0,60),eligible:rows.slice(0,60),
    });
    if(url.pathname.endsWith("/detection-settings"))return Response.json({disabled:[]});
    return Response.json({data:[]});
  }));
  mount("fresh-optimization");const user=userEvent.setup();
  await screen.findByText("Channel 1",{selector:"strong"});
  await user.click(screen.getByRole("button",{name:"检测全部模型 · 61 个渠道"}));
  const failure = await screen.findByRole("dialog", { name: "批量检测失败" });
  expect(within(failure).getByText("渠道配置或检测结果已变化，请重新读取")).toBeVisible();
  await user.click(within(failure).getByRole("button", { name: "知道了" }));
  const before=calls.filter(u=>u.searchParams.has("selection")).length;
  await user.click(screen.getByRole("button",{name:"优化渠道模型"}));
  const dialog=await screen.findByRole("dialog",{name:"优化渠道模型"});
  expect(within(dialog).getByText(/分析当前筛选的全部 60 个渠道/)).toBeVisible();
  const rules = within(dialog).getByText("优化规则").closest("details")!;
  expect(rules).not.toHaveAttribute("open");
  await user.click(within(rules).getByText("优化规则"));
  expect(within(rules).getByText(/预览使用点击时读取的最新检测结果/)).toBeVisible();
  expect(calls.filter(u=>u.searchParams.has("selection"))).toHaveLength(before);
  expect(calls.filter(u=>u.pathname.endsWith("/optimization-preview"))).toHaveLength(1);
  await user.click(within(dialog).getByRole("button",{name:"关闭优化预览"}));
  expect(screen.getAllByRole("row")).toHaveLength(26);
});

it.each([
 ["检测全部模型", "checks", ""], ["降智检测", "quality-checks", "quality"],
 ["压缩检测", "compaction-checks", "compaction"], ["Tool use 检测", "tool-use-checks", "tool-use"],
])("%s skips disabled channels on every page, while explicit row detection still works",async(label,endpoint,project)=>{
 const writes:{path:string;body:{manual?:boolean;targets:{group_id:number}[]}}[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input),path=url.pathname;
  if(path.endsWith("/detection-settings"))return Response.json({disabled:[1,26,61].map(group_id=>({account_id:account.id,group_id}))});
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(init?.method==="POST"){writes.push({path,body:JSON.parse(String(init.body))});return Response.json({queued:58});}
  if(path.endsWith("/channel-management/query"))return Response.json(url.searchParams.has("selection")?selection():page(Number(url.searchParams.get("cursor")||0)));
  return Response.json({data:[]});
 }));
 mount(`disabled-${endpoint}`);const user=userEvent.setup();
 const toggle=await screen.findByRole("switch",{name:"Owned site Channel 1 的批量检测开关"});
 await waitFor(()=>expect(toggle).not.toBeChecked());
 if(project) await user.selectOptions(screen.getByLabelText("选择检测项目"),project);
 await user.click(screen.getByRole("button",{name:`${label} · 61 个渠道`}));
 const preview=await screen.findByRole("dialog",{name:"核对批量检测范围"});
 expect(within(preview).getByText(/58 \/ 61 个渠道；跳过 3 个/)).toBeVisible();
 expect(writes).toHaveLength(0);
 await user.click(within(preview).getByRole("button",{name:"确认提交检测"}));
 await waitFor(()=>expect(writes).toHaveLength(1));
 expect(writes[0].path).toBe(`/analytics/v1/sub2api/${endpoint}`);
 expect(writes[0].body.manual).not.toBe(true);
 expect(writes[0].body.targets.map(t=>t.group_id)).toEqual(Array.from({length:61},(_,i)=>i+1).filter(n=>![1,26,61].includes(n)));
 await user.click(screen.getByRole("button",{name:"检测 Owned site Channel 1"}));
 await waitFor(()=>expect(writes).toHaveLength(2));
 expect(writes[1].body.manual).toBe(true);expect(writes[1].body.targets.map(t=>t.group_id)).toEqual([1]);
});

it("offers all four projects and queues only eligible fingerprints across management pages", async () => {
  const model = "deepseek-v4.1-flash-1017";
  const member: ManagedChannel = {
    kind: "configured", source_id: "gateway", source_name: "Gateway", provider: "native-deepseek", name: "Native DeepSeek",
    models: [model], engine: "gpt", account_ids: [], account_id: "", group_id: 0, api_key_id: "", key_position: 0,
    key_prefix: "", positions: {}, revision: "native", manageable: false,
  };
  const noModel = { ...member, provider: "native-other", name: "Native other", models: ["gpt-5.5"] };
  const full: ManagementRow[] = [
    ...rows.slice(0, 59),
    ...[member, noModel].map((configured, i) => ({ ...rows[59 + i], account: { ...account, id: "", name: "未归属渠道" }, accountIds: [], configured })),
  ];
  const snapshot: ManagementSelection = {
    ...selection(), rows: full, eligible: full, input: {
      ...selection().input, channels: [member, noModel],
      fingerprints: [{ account_id: account.id, group_id: 27, model, state: "running", message: "", revision: "", result: null }],
    },
  };
  const writes: {path: string; body: unknown}[] = [];
  const selections: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input), path = url.pathname;
    if (path.endsWith("/detection-settings")) return Response.json({ disabled: [1, 26, 59].map(group_id => ({ account_id: account.id, group_id })) });
    if (path.endsWith("/accounts/progress")) return idleProgress();
    if (init?.method === "POST") {
      writes.push({ path, body: JSON.parse(String(init.body)) });
      return Response.json({ accepted: true }, { status: 202 });
    }
    if (path.endsWith("/channel-management/query")) {
      const token = url.searchParams.get("selection");
      if (token) { selections.push(token); return Response.json(snapshot); }
      const offset = Number(url.searchParams.get("cursor") || 0);
      const current = page(offset);
      return Response.json({ ...current, rows: full.slice(offset, offset + 25), context: { ...current.context, fingerprint_models: [model] } });
    }
    return Response.json({ data: [] });
  }));

  const user = userEvent.setup(), owner = "manage-fingerprint";
  let view = mount(owner);
  const picker = screen.getByLabelText("选择检测项目");
  expect(within(picker).getAllByRole("option").map(option => option.textContent)).toEqual(["降智检测", "指纹检测", "Tool use 检测", "压缩检测", "Fast 检测", "Ultrafast 检测"]);
  await user.selectOptions(picker, "fingerprint");
  await user.selectOptions(await screen.findByLabelText("检测模型筛选"), "gpt-5.5");
  await waitFor(() => expect(screen.getByRole("button", { name: "指纹检测 · 61 个渠道" })).toBeDisabled());
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), model);
  const action = screen.getByRole("button", { name: "指纹检测 · 61 个渠道" });
  await waitFor(() => expect(action).toBeEnabled());
  expect(writes).toHaveLength(0);
  view.unmount();
  view = mount(owner);
  expect(screen.getByLabelText("选择检测项目")).toHaveValue("fingerprint");
  await waitFor(() => expect(screen.getByRole("button", { name: "指纹检测 · 61 个渠道" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "下一页" }));
  const group = screen.getByRole("group", { name: "专项检测" });
  expect(within(group).getAllByRole("button")).toHaveLength(1);
  const submit = within(group).getByRole("button", { name: "指纹检测 · 61 个渠道" });
  await waitFor(() => expect(submit).toBeEnabled());
  await user.click(submit);
  expect(writes).toHaveLength(0);
  await user.click(await screen.findByRole("button",{name:"确认提交检测"}));
  await screen.findByText(/已提交 56 项指纹检测/);
  expect(selections).toEqual(["signed-all","signed-all"]);
  expect(writes).toEqual([{
    path: "/analytics/v1/model-fingerprints",
    body: { targets: [
      ...rows.slice(0, 59).map(row => row.target.group_id).filter(id => ![1, 26, 27, 59].includes(id)).map(group_id => ({ account_id: account.id, group_id, model })),
      { source_id: member.source_id, provider: member.provider, model },
    ] },
  }]);
  view.unmount();
});

it.each([
  { tier: "ultrafast", label: "Ultrafast", failure: 0, stale: false },
  { tier: "fast", label: "Fast", failure: 0, stale: false },
  { tier: "ultrafast", label: "Ultrafast", failure: 502, stale: false },
  { tier: "ultrafast", label: "Ultrafast", failure: 409, stale: false },
  { tier: "ultrafast", label: "Ultrafast", failure: 200, stale: false },
  { tier: "ultrafast", label: "Ultrafast", failure: 0, stale: true },
] as const)("$tier covers all filtered pages (second-batch response: $failure, stale scope: $stale)", async ({tier, label, failure, stale}) => {
  const owner = `tier-batches-${tier}-${failure}-${stale}`;
  const model = "gpt-6.2-sol";
  const scope = "a".repeat(64);
  saveSubFilters(owner, {...subFilterDefaults, model, availability: "success"});
  const matches = Array.from({length: 63}, (_, i) => {
    const row = {...rows[0], id: `row-${i + 1}`, target: target(i + 1)};
    const configured: ManagedChannel = {
      source_id: "gateway", source_name: "Gateway", provider: `native-${i + 1}`,
      name: row.target.name, engine: "gpt", service_tier_scope: scope, models: [model, "gpt-5.5"],
      account_ids: [], account_id: "", group_id: 0, api_key_id: "", key_position: 0,
      key_prefix: "", positions: {}, revision: "native", manageable: false,
    };
    return {...row, account: {...account, id: "", name: "Native"}, accountIds: [], configured};
  });
  // Disabled or already-running rows may be displayed but must not be sent.
  const candidateIDs = matches.map(row => row.configured.provider).filter(id => id !== "native-2" && id !== "native-28");
  const alias = "public-sol";
  const site = {...matches[62], account, accountIds: [account.id], configured: undefined};
  const binding = {...matches[62].configured, account_id: account.id, group_id: 63,
    models: [alias, "gpt-5.5"], model_mappings: {[alias]: model}};
  const expectedTargets = candidateIDs.map(provider => ({source_id: "gateway", provider, models: [provider === binding.provider ? alias : model], expected_scope: scope}));
  const running = {
    source_id: "gateway", provider: "native-28", kind: tier, model, state: "running", fingerprint: scope,
    message: "", result: null, history: undefined,
  };
  const allRows: ManagementRow[] = [...matches.slice(0, -1), site];
  const snapshot: ManagementTierSelection = {
    format: "management-tier-selection-v1", revision: page().revision, read_at_ms: 1,
    eligible: allRows, total: allRows.length, eligible_count: allRows.length, imports: [binding], checks: [running],
  };
  const writes: { kind: string; confirmed: boolean; manual: boolean; targets: typeof expectedTargets }[] = [];
  const tokens: string[] = [];
  let releaseFirst!: (response: Response) => void, tierReads = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input);
    if (init?.method === "POST") {
      expect(url.pathname).toBe("/analytics/v1/channel-management/checks");
      writes.push(JSON.parse(String(init.body)));
      if (writes.length === 1) return new Promise<Response>(resolve => { releaseFirst = resolve; });
      if (writes.length === 2 && failure)
        return failure === 200 ? Response.json({queued: "invalid"}) : new Response(
          failure === 409 ? "渠道配置已变化" : "<html>Bad Gateway</html>", {status: failure},
        );
      return Response.json({queued: writes.length === 2 ? 0 : 1}, {status: 202});
    }
    if (url.pathname.endsWith("/accounts/progress")) return idleProgress();
    if (url.pathname.endsWith("/detection-settings"))
      return Response.json({disabled: [{source_id: "gateway", provider: "native-2"}]});
    if (url.pathname.endsWith("/checks") && url.searchParams.get("kind") === "service-tiers") {
      tierReads++;
      return Response.json({data: [running]});
    }
    if (url.pathname.endsWith("/channel-management/query")) {
      expect(JSON.parse(url.searchParams.get("filters")!)).toMatchObject({model, availability: "success"});
      const token = url.searchParams.get("selection");
      if (token) {
        // The production full diagnostic read times out. Both the preview and
        // pre-submit verification must use the targeted selection API.
        if (url.searchParams.get("check_selection") !== "tier") return new Response(null, {status: 503});
        tokens.push(token);
        return Response.json(stale && tokens.length === 2
          ? {...snapshot, imports: [{...binding, service_tier_scope: "b".repeat(64)}]}
          : snapshot);
      }
      const offset = Number(url.searchParams.get("cursor") || 0);
      return Response.json({...page(offset), rows: allRows.slice(offset, offset + 25), total: snapshot.total, eligible_count: snapshot.eligible_count});
    }
    return Response.json({data: []});
  }));

  localStorage.setItem(managementColumnsStorageKey, JSON.stringify(["model", "availability", tier]));
  try { mount(owner); }
  finally { localStorage.removeItem(managementColumnsStorageKey); }
  const user = userEvent.setup();
  await user.selectOptions(screen.getByLabelText("选择检测项目"), tier);
  await user.click(await screen.findByRole("button", {name: "下一页"}));
  await screen.findByText("Channel 26", {selector: "strong"});
  const run = screen.getByRole("button", {name: `${label} 检测 · 63 个渠道`});
  await waitFor(() => expect(run).toBeEnabled());
  const table = screen.getByRole("region", {name: "渠道管理表，可横向滚动"});
  const toolbar = table.previousElementSibling;
  await user.click(run);
  const preview = await screen.findByRole("dialog", {name: "核对批量检测范围"});
  expect(within(preview).getByText(/本次计划 61 \/ 63 个渠道/)).toBeVisible();
  expect(within(preview).getByText("最多 61 次可能计费的上游请求")).toBeVisible();
  expect(writes).toHaveLength(0);

  await user.click(within(preview).getByRole("button", {name: "确认提交检测"}));
  if (stale) {
    const error = await screen.findByRole("dialog", {name: "批量检测失败"});
    expect(error).toHaveTextContent("档位待检测范围已变化，请重新预览。");
    expect(tokens).toEqual(["signed-all", "signed-all"]);
    expect(writes).toHaveLength(0);
    expect(table.previousElementSibling).toBe(toolbar);
    return;
  }
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(within(preview).getByRole("button", {name: "正在核对并提交…"})).toBeDisabled();
  const readsBeforeReceipt = tierReads;
  await act(async () => { releaseFirst(Response.json({queued: 29}, {status: 202})); });
  if (!failure) {
    const notice = await screen.findByText(`${label} 检测已提交 · 排队 30 项`);
    expect(notice.closest('[role="status"]')?.parentElement).toBe(document.body);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  } else {
    const dialog = await screen.findByRole("dialog", {
      name: failure === 409 ? "批量检测未全部提交" : "批量检测提交结果未确认",
    });
    const message = within(dialog).getByRole("alert");
    expect(message).toHaveTextContent("前 30 项已收到回执，新增排队 29 项。");
    expect(message).toHaveTextContent(failure === 409 ? "本批 30 项未入队（渠道配置已变化）" : "本批 30 项提交结果未确认");
    expect(message).toHaveTextContent("后续 1 项未提交。");
    if (failure !== 409) expect(message).toHaveTextContent("避免重复提交");
    await user.click(within(dialog).getByRole("button", {name: "知道了"}));
  }
  expect(tokens).toEqual(["signed-all", "signed-all"]);
  expect(writes).toEqual([expectedTargets.slice(0, 30), expectedTargets.slice(30, 60),
    ...(!failure ? [expectedTargets.slice(60)] : [])].map(targets => ({kind: tier, confirmed: true, manual: false, targets})));
  await waitFor(() => expect(tierReads).toBeGreaterThan(readsBeforeReceipt));
  expect(screen.getByRole("region", {name: "渠道管理表，可横向滚动"})).toBe(table);
  expect(table.previousElementSibling).toBe(toolbar);
  expect(toolbar).toHaveClass("management-toolbar");
});

it("persists each channel switch, keeps failed saves unchanged and restores it after a fresh page read",async()=>{
 let disabled:{account_id:string;group_id:number}[]=[];let fail=false;
 const patches:unknown[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input),path=url.pathname;
  if(path.endsWith("/detection-settings")){
   if(init?.method==="PATCH"){
    const body=JSON.parse(String(init.body));patches.push(body);
    if(fail)return new Response("检测开关保存失败",{status:503});
    disabled=body.enabled?[]:body.targets;return Response.json({saved:true});
   }
   return Response.json({disabled});
  }
  expect(init?.method||"GET").toBe("GET");
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(path.endsWith("/channel-management/query"))return Response.json(page());
  return Response.json({data:[]});
 }));
 const user=userEvent.setup();let view=mount("switch-persist");
 let toggle=await screen.findByRole("switch",{name:"Owned site Channel 1 的批量检测开关"});
 await waitFor(()=>expect(toggle).toBeEnabled());expect(toggle).toBeChecked();
 await user.click(toggle);await waitFor(()=>expect(toggle).not.toBeChecked());
 expect(patches).toEqual([{enabled:false,targets:[{account_id:"owned",group_id:1}]}]);
 expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
 expect(screen.getByRole("switch",{name:"Owned site Channel 2 的批量检测开关"})).toBeChecked();
 view.unmount();view=mount("switch-persist");
 toggle=await screen.findByRole("switch",{name:"Owned site Channel 1 的批量检测开关"});
 await waitFor(()=>expect(toggle).not.toBeChecked());
 fail=true;await user.click(toggle);
 await screen.findByText("检测开关保存失败");expect(toggle).not.toBeChecked();
 fail=false;await user.click(toggle);await waitFor(()=>expect(toggle).toBeChecked());
 view.unmount();
});

it("blocks bulk detection when switch settings cannot be read, but permits explicit manual detection",async()=>{
 const writes:unknown[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input),path=url.pathname;
  if(path.endsWith("/detection-settings"))return new Response("settings unavailable",{status:503});
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(init?.method==="POST"){writes.push(JSON.parse(String(init.body)));return Response.json({queued:true});}
  if(path.endsWith("/channel-management/query"))return Response.json(page());
  return Response.json({data:[]});
 }));
 mount("settings-unavailable");const user=userEvent.setup();
 await screen.findByText("检测开关读取失败，已暂停批量检测。");
 expect(screen.getByRole("button",{name:"检测全部模型 · 61 个渠道"})).toBeDisabled();
 for(const [project, label] of [["quality","降智检测"],["compaction","压缩检测"],["tool-use","Tool use 检测"]]) {
  await user.selectOptions(screen.getByLabelText("选择检测项目"),project);
  expect(screen.getByRole("button",{name:`${label} · 61 个渠道`})).toBeDisabled();
 }
 await user.click(screen.getByRole("button",{name:"检测 Owned site Channel 1"}));
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]).toMatchObject({manual:true,targets:[{account_id:"owned",group_id:1}]});
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
  await screen.findByText(/^暂时无法读取渠道：/);
  expect(failed).toBe(true);
  expect(screen.queryByText("没有匹配的渠道")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重试渠道结果读取" }));
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
  await openSites();
 expect(screen.getByRole("button",{name:"一键同步并检测"})).toBeDisabled();
 expect(screen.getByRole("button",{name:"一键同步"})).toBeEnabled();
 await user.click(screen.getByRole("button",{name:"一键同步"}));
 await screen.findByText("同步检测服务待更新，请稍后重试。");
 expect(writes).toEqual(["/analytics/v1/sub2api/accounts/sync-only"]);
 await openChannels();
 expect(screen.getByText("Channel 1",{selector:"strong"})).toBeVisible();
});

it.each([202,404])("submits a single site's saved model selection, never a legacy all-model sync (HTTP %s)",async status=>{
 const owner="single-sync-scope",selected=SUB_MODELS.slice(0,36);
 const catalog=[...SUB_MODELS,...Array.from({length:291-SUB_MODELS.length},(_,i)=>`site-extra-${i}`)];
 expect(selected).toHaveLength(36);
 expect(catalog).toHaveLength(291);
 saveSubModels(owner,selected,catalog);
 let pageReads=0,progressReads=0;
 const writes:{path:string;body:unknown}[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const path=new URL(input).pathname;
  if(path.endsWith("/accounts/progress")){progressReads++;return idleProgress();}
  if(init?.method==="POST"){
   writes.push({path,body:init.body?JSON.parse(String(init.body)):null});
   return status===202?Response.json({queued:true},{status}):new Response("404 page not found",{status});
  }
  if(path.endsWith("/channel-management/query")){pageReads++;return Response.json({...page(),models:catalog,detection_models:catalog});}
  return Response.json({data:[]});
 }));
 mount(owner);const user=userEvent.setup();
 await screen.findByText("Channel 1",{selector:"strong"});
 // The list's model filter is not the saved account-sync detection scope.
 await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-5.5");
 await waitFor(()=>expect(pageReads).toBe(2));
 await openSites();
 const row=document.querySelector<HTMLElement>('[role="listitem"][aria-label="Owned site"]')!;
 const button=within(row).getByRole("button",{name:"同步并检测"});
 expect(button).toHaveAttribute("title","仅检测已勾选的 36 个模型");
 await waitFor(()=>expect(progressReads).toBeGreaterThan(0));
 const before=progressReads;
 await user.click(button);
 if(status===202){
  await screen.findByText("已提交 1 个账号，正在后台同步并检测。");
  await waitFor(()=>expect(progressReads).toBeGreaterThan(before));
  expect(pageReads).toBe(2);
 }else await screen.findByText("同步检测服务待更新，请稍后重试。");
 expect(writes).toEqual([{path:"/analytics/v1/sub2api/accounts/owned/sync-selected",body:{models:selected}}]);
});

it("disables single-account sync when the saved detection selection is empty",async()=>{
 const owner="single-sync-empty";saveSubModels(owner,[]);
 const writes:string[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const path=new URL(input).pathname;
  if(init?.method==="POST")writes.push(path);
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(path.endsWith("/channel-management/query"))return Response.json(page());
  return Response.json({data:[]});
 }));
 mount(owner);
 await screen.findByText("Channel 1",{selector:"strong"});
  await openSites();
 expect(within(document.querySelector<HTMLElement>('[role="listitem"][aria-label="Owned site"]')!).getByRole("button",{name:"同步并检测"})).toBeDisabled();
 expect(writes).toEqual([]);
});

it.each(["submission", "progress"])("keeps other sites actionable while one site waits for %s", async waiting => {
  const accounts = [account, {...account, id: "second", name: "Second site"}];
  let finishFirst!: (response: Response) => void;
  let firstReadWaiting = false;
  const writes: string[] = [];
  const progress = () => Response.json({data: accounts.map(site => ({
    ...site, progress: {total:0,completed:0,queued:0,running:0,interrupted:0,running_models:[],groups:[]},
  })), read_at_ms: Date.now()});
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const path = new URL(input).pathname;
    if (path.endsWith("/accounts/progress")) {
      if (waiting === "progress" && writes.length === 1) {
        firstReadWaiting = true;
        return new Promise<Response>(resolve => { finishFirst = resolve; });
      }
      return progress();
    }
    if (path.endsWith("/sync-selected") && init?.method === "POST") {
      writes.push(path);
      if (waiting === "submission" && writes.length === 1) {
        return new Promise<Response>(resolve => { finishFirst = resolve; });
      }
      return Response.json({queued: true}, {status: 202});
    }
    if (path.endsWith("/channel-management/query")) return Response.json({...page(), accounts});
    return Response.json({data: []});
  }));
  mount(`independent-site-${waiting}`);
  await openSites();
  const user = userEvent.setup();
  const sync = (name: string) => within(screen.getByRole("listitem", {name})).getByRole("button", {name: "同步并检测"});
  await screen.findByRole("listitem", {name: "Owned site"});
  await user.click(sync("Owned site"));
  if (waiting === "progress") await waitFor(() => expect(firstReadWaiting).toBe(true));
  expect(sync("Owned site")).toBeDisabled();
  expect(sync("Second site")).toBeEnabled();
  await user.click(sync("Second site"));
  expect(writes).toEqual([
    "/analytics/v1/sub2api/accounts/owned/sync-selected",
    "/analytics/v1/sub2api/accounts/second/sync-selected",
  ]);
  await act(async () => finishFirst(waiting === "progress" ? progress() : Response.json({queued:true}, {status:202})));
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
 const accountRow=document.querySelector('[role="listitem"][aria-label="Owned site"]')!;
 const options=screen.getByLabelText("倍率上限筛选").innerHTML;
 const table=screen.getByRole("table");
 const firstRow=screen.getByText("Channel 1",{selector:"strong"});
 if(change === "filter") await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-5.5");
 else await user.click(screen.getByRole("button",{name:"下一页"}));
 await waitFor(()=>expect(pageReads).toBe(2));
 expect(document.querySelector<HTMLElement>('[role="listitem"][aria-label="Owned site"]')!).toBe(accountRow);
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
 expect(document.querySelector<HTMLElement>('[role="listitem"][aria-label="Owned site"]')!).toBe(accountRow);
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
 const table=screen.getByRole("table"),accountRow=document.querySelector('[role="listitem"][aria-label="Owned site"]')!;
 const row=screen.getByRole("row",{name:"查看 Owned site Channel 26 渠道详情"});
 await act(async()=>{await controlRequest(path,{method});});
 await waitFor(()=>expect(reads).toBe(3));
 expect(document.querySelector<HTMLElement>('[role="listitem"][aria-label="Owned site"]')!).toBe(accountRow);
 expect(document.querySelector(".sub-results-table table")).toBe(table);
 expect(table.parentElement).toHaveAttribute("inert");
 expect(row).toBeVisible();
 expect(within(row).getByRole("button",{name:"检测 Owned site Channel 26",hidden:true})).toBeDisabled();
 expect(screen.queryByRole("status",{name:"加载渠道检测"})).not.toBeInTheDocument();
 expect(screen.getByText("正在更新渠道结果，暂显示更新前内容…")).toBeVisible();
 await act(async()=>{finish(Response.json({...page(25),revision:"updated",read_at_ms:2}));});
 await waitFor(()=>expect(table.parentElement).not.toHaveAttribute("inert"));
 expect(screen.getByRole("table")).toBe(table);
 expect(screen.getByText("Channel 26",{selector:"strong"}).closest("tr")).toBe(row);
});

it("keeps other rows actionable after a four-model check while its row details are still loading", async () => {
 const owner = "concurrent-four-model-checks";
 const models = SUB_MODELS.slice(0, 4);
 saveSubModels(owner, models);
 const checks: { targets: { group_id: number; models: string[] }[] }[] = [];
 const details = new Map<number, (response: Response) => void>();
 let pageReads = 0;
 let switches = 0;
 vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
  const path = new URL(input).pathname;
  if (path.endsWith("/sub2api/checks")) {
   checks.push(JSON.parse(String(init?.body)));
   return Response.json({queued: 4}, {status: 202});
  }
  if (path.endsWith("/channel-management/query")) {
   pageReads++;
   return Response.json(page());
  }
  const group = /\/groups\/(\d+)\/details$/.exec(path);
  if (group) return new Promise<Response>(resolve => { details.set(Number(group[1]), resolve); });
  if (path.endsWith("/accounts/progress")) return idleProgress();
  if (path.endsWith("/detection-settings")) {
   if (init?.method === "PATCH") switches++;
   return Response.json({disabled: []});
  }
  return Response.json({data: []});
 }));
 const view = mount(owner);
 const user = userEvent.setup();
 const first = await screen.findByRole("button", {name: "检测 Owned site Channel 1"});
 const second = screen.getByRole("button", {name: "检测 Owned site Channel 2"});
 const third = screen.getByRole("row", {name: "查看 Owned site Channel 3 渠道详情"});
 expect(first).toHaveTextContent("检测已选 4 个模型");
 await user.click(first);
 await waitFor(() => expect(details.has(1)).toBe(true));
 expect(checks[0].targets[0]).toMatchObject({group_id: 1, models});
 expect(screen.getByRole("table").parentElement).not.toHaveAttribute("inert");
 expect(first).toBeDisabled();
 expect(second).toBeEnabled();
 expect(within(third).getByRole("button", {name: "添加到渠道"})).toBeEnabled();
 expect(screen.getByRole("button", {name: "刷新渠道管理"})).toBeEnabled();
 expect(screen.getByRole("button", {name: "下一页"})).toBeEnabled();
 await user.click(first);
 await user.click(within(third).getByRole("switch"));
 await user.click(second);
 expect(switches).toBe(1);
 expect(checks.map(check => check.targets[0].group_id)).toEqual([1, 2]);
 await waitFor(() => expect(details.has(2)).toBe(true));
 const finish = (id: number) => details.get(id)!(Response.json({data: [{...account, targets: [target(id)]}]}));
 await act(async () => finish(1));
 await waitFor(() => expect(first).toBeEnabled());
 expect(second).toBeDisabled();
 expect(within(third).getByRole("button", {name: "检测 Owned site Channel 3"})).toBeEnabled();
 await act(async () => finish(2));
 expect(screen.getByRole("table").parentElement).not.toHaveAttribute("inert");
 await waitFor(() => expect(second).toBeEnabled());
 expect(pageReads).toBe(1);
 view.unmount();
});

it("updates selected-model rows independently during a mixed batch larger than the progress watch limit", async () => {
  const owner = "large-live-mixed-batch", model = "gpt-6.2-sol", user = userEvent.setup();
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  saveSubFilters(owner, {...subFilterDefaults, model});
  const native: ManagedChannel = {
    source_id:"src", source_name:"Native", provider:"native", name:"Native", models:[model], engine:"gpt",
    account_id:"", account_ids:[], group_id:0, api_key_id:"key", key_position:0, key_prefix:"", positions:{}, revision:"current", manageable:true,
  };
  const all = Array.from({length: 300}, (_, i) => {
    const a = {...account, id: `account-${i}`, name:`Site ${i}`};
    return {...rows[0], id: `large-${i}`, account: a, accountIds:[a.id], target: target(1)};
  });
  const siteRows = [all[0], {...all[0], id:"large-sibling", target:target(2)}, ...all.slice(1)];
  const nativeRow = {...rows[0], id:"native-mixed", account:{...account,id:"",name:"Native"}, accountIds:[], configured:native};
  const eligible = [...siteRows, nativeRow];
  let pageReads = 0, tick = 1;
  let phase: "idle" | "queued" | "running" | "done" = "idle";
  let finishSite!: (value:Response)=>void, finishNative!: (value:Response)=>void;
  vi.stubGlobal("fetch", vi.fn(async (input:string, init?:RequestInit) => {
    const url = new URL(input);
    if (url.pathname.endsWith("/detection-settings")) return Response.json({disabled:[]});
    if (url.pathname.endsWith("/channel-management/query")) {
      if (url.searchParams.has("selection")) return Response.json({format:"management-check-selection-v1",revision:"current",read_at_ms:2,total:eligible.length,eligible_count:eligible.length,eligible});
      pageReads++;
      const shown = [...siteRows.slice(0,24),nativeRow];
      return Response.json({...page(),accounts:all.map(row=>row.account),total:eligible.length,eligible_count:eligible.length,rows:shown,
        context:{...page().context,fingerprints:Object.fromEntries(shown.map(row=>[row.id,[]]))}});
    }
    if (init?.method === "POST" && url.pathname.endsWith("/sub2api/checks")) {
      expect(JSON.parse(String(init.body)).targets).toHaveLength(301);
      return new Promise<Response>(resolve => {finishSite=resolve;});
    }
    if (init?.method === "POST" && url.pathname.endsWith("/channel-management/checks")) return new Promise<Response>(resolve => {finishNative=resolve;});
    if (url.pathname.endsWith("/accounts/progress")) {
      const watched: string[] = JSON.parse(url.searchParams.get("accounts")!);
      expect(watched).toHaveLength(23);
      return Response.json({data:all.map(({account:a},i)=>({...a,state:phase === "idle" || phase === "done" ? "idle" : "running",job_kind:"check",progress:watched.includes(a.id) ? {
        total:2,completed:Number(phase==="done"),queued:0,running:0,interrupted:0,running_models:[],groups:[1,2].map(group_id => ({group_id,state: i===0&&group_id===1&&phase!=="queued"?phase:"idle",waiting:phase!=="idle"&&(group_id===2||phase==="queued")?1:0,model_version:tick})),
      } : undefined})),read_at_ms:++tick});
    }
    const match = /\/accounts\/(.*?)\/groups\/(\d+)\/details$/.exec(url.pathname);
    if (match) {
      const a = all.find(row=>row.account.id===match[1])!.account, group = Number(match[2]);
      const state = a.id===all[0].account.id && group===1 ? phase : "queued";
      const result = state === "done" ? {model,checked_at:20,verdict:"not_applicable",
        availability:{status:"success",text:"",ttft_ms:1,duration_ms:1},quality:{status:"skipped",text:"",ttft_ms:null,duration_ms:0},
        service_tiers:{fast:{status:"supported",checked_at:Math.floor(Date.now()/1000)}}} : null;
      return Response.json({data:[{...a,targets:[{...target(group),state,models:[{model,state,message:"",result}]}]}]});
    }
    return Response.json({data:[]});
  }));
  localStorage.setItem(managementColumnsStorageKey, JSON.stringify(["model", "availability", "fast", "ultrafast"]));
  try { mount(owner,client); }
  finally { localStorage.removeItem(managementColumnsStorageKey); }
  const button = await screen.findByRole("button", {name:`检测所选模型 · ${eligible.length} 个渠道`});
  await waitFor(()=>expect(button).toBeEnabled());
  const row = screen.getByRole("row", {name:"查看 Site 0 Channel 1 渠道详情"});
  const sibling = screen.getByRole("row", {name:"查看 Site 0 Channel 2 渠道详情"});
  await user.click(button);
  await user.click(within(await screen.findByRole("dialog", {name:"核对批量检测范围"})).getByRole("button", {name:"确认提交检测"}));
  expect(await within(row).findByText("提交中")).toBeVisible();
  phase="queued";
  await act(async()=>finishSite(Response.json({queued:301}, {status:202})));
  expect(await within(sibling).findByText("排队中")).toBeVisible();
  phase="running";
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"],type:"active"});});
  expect(await within(row).findByText("检测中", {selector:".check-status"})).toBeVisible();
  phase="done";
  await act(async()=>{await client.refetchQueries({queryKey:["sub-sync-progress"],type:"active"});});
  expect(await within(row).findByText("可用", {selector:".check-status"})).toBeVisible();
  expect(within(row).getByText("已验证支持")).toBeVisible();
  expect(within(sibling).getByText("排队中")).toBeVisible();
  expect(screen.queryByText("批量检测已提交")).not.toBeInTheDocument();
  expect(pageReads).toBe(1);
  await act(async()=>finishNative(Response.json({queued:1}, {status:202})));
  expect(await screen.findByText("批量检测已提交")).toBeVisible();
});

it.each(["success","conflict","unknown"])("closes the page-two import immediately and preserves other actions (%s)",async outcome=>{
 const patience={timeout:7000};
 const probe={status:"success",text:"fixture",ttft_ms:1,duration_ms:2,protocol:"responses" as const};
 const result={model:"gpt-5.5",checked_at:1,availability:probe,quality:probe,verdict:"not_applicable"};
 const cursors:string[]=[];let posts=0,bindingReads=0,finish!:(response:Response)=>void,finishPost!:(response:Response)=>void;
 const total=27;
 const readyPage=(offset:number)=>{
  const p=page(offset);
  return {...p,total,eligible_count:total,next_cursor:offset?undefined:p.next_cursor,rows:p.rows.slice(0,total-offset).map(r=>({...r,target:{...r.target,models:[{model:"gpt-5.5",state:"done",message:"",result}]},checks:[{model:"gpt-5.5",state:"done",message:"",result}]}))};
 };
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  const url=new URL(input),path=url.pathname;
  if(path.endsWith("/sub2api/channels")&&init?.method==="POST"){posts++;return new Promise<Response>(resolve=>{finishPost=resolve;});}
  if(path.endsWith("/sub2api/channels")){
   bindingReads++;
   expect(url.searchParams.get("view")).toBe("bindings");
   expect(url.searchParams.get("account_id")).toBe("owned");
   expect(["26","27"]).toContain(url.searchParams.get("group_id"));
   return Response.json({data:[],labels:{},unavailable_sources:[]});
  }
  if(path.endsWith("/accounts/progress"))return idleProgress();
  if(path.endsWith("/channel-management/query")){
   if(url.searchParams.has("selection")) return Response.json({
    ...selection(),revision:"after-import",total,eligible_count:total,rows:rows.slice(0,total),eligible:rows.slice(0,total),
   });
   const cursor=url.searchParams.get("cursor")||"";cursors.push(cursor);
   if(posts && outcome==="success"){
    expect(cursor).toBe("25");expect(url.searchParams.get("refresh_cursor")).toBe("1");
    return new Promise<Response>(resolve=>{finish=resolve;});
   }
   return Response.json(readyPage(Number(cursor||0)));
  }
  if(path.endsWith("/sources"))return Response.json({data:[{id:"fixture",name:"Fixture"}]});
  if(path.endsWith("/detection-settings"))return Response.json({disabled:[]});
  if(path.endsWith("/import-status"))return Response.json({state:"idle",job_kind:"",can_import:true});
  if(path.endsWith("/access"))return Response.json({available:true,status:"ok"});
  if(path.endsWith("/channel-options"))return Response.json({supported:true,revision:"r1",keys:[
   {key_id:"fixture-key",position:1,prefix:"fixture"},
   {key_id:"second-key",position:2,prefix:"second"},
  ],channels:[]});
  return Response.json({data:[],labels:{},unavailable_sources:[]});
 }));
 mount("import-second-page");const user=userEvent.setup();
 await screen.findByText("Channel 1",{selector:"strong"},patience);
 await user.click(screen.getByRole("button",{name:"下一页"}));
 const row=await screen.findByRole("row",{name:"查看 Owned site Channel 26 渠道详情"},patience);
 const table=screen.getByRole("table");
 await user.click(within(row).getByRole("button",{name:"添加到渠道"}));
 const source=await screen.findByLabelText("添加到 uni-api 来源",{},patience);
 const d=within(screen.getByRole("dialog"));
 expect(cursors).toEqual(["","25"]);
 expect(bindingReads).toBe(1);
 expect(source).toHaveValue("fixture");
 await waitFor(()=>expect(d.getByLabelText("添加到 API key")).toHaveValue("fixture-key"),patience);
 if(outcome==="conflict") await user.selectOptions(d.getByLabelText("添加到 API key"),"second-key");
 await waitFor(()=>expect(d.getByRole("button",{name:"添加到渠道"})).toBeEnabled(),patience);
 await user.click(d.getByRole("button",{name:"添加到渠道"}));
 await waitFor(()=>expect(posts).toBe(1));
 expect(screen.queryByRole("dialog",{name:"添加到渠道"})).not.toBeInTheDocument();
 expect(document.body.style.pointerEvents).not.toBe("none");
 expect(screen.getByText(/正在后台添加/)).toBeVisible();
 expect(document.querySelector(".sub-results-table table")).toBe(table);
 expect(document.querySelector(".sub-pagination")).toHaveTextContent("2 / 2");
 const otherRow=screen.getByRole("row",{name:"查看 Owned site Channel 27 渠道详情"});
 const otherAdd=within(otherRow).getByRole("button",{name:"添加到渠道"});
 await user.click(otherAdd);
 const otherDialog=await screen.findByRole("dialog",{name:"添加到渠道"},patience);
 await user.click(within(otherDialog).getByRole("button",{name:"关闭添加渠道"}));
 if(outcome!=="success"){
  await act(async()=>finishPost(new Response("渠道状态已变化",{status:outcome==="conflict"?409:502})));
  await screen.findByText(outcome==="conflict"?/添加失败：渠道状态已变化/:/添加结果未确认：渠道状态已变化/);
  expect(screen.queryByRole("dialog",{name:"添加到渠道"})).not.toBeInTheDocument();
  expect(otherAdd).toBeEnabled();
  if(outcome==="conflict"){
   await user.click(screen.getByRole("button",{name:"恢复添加配置"}));
   const restored=within(await screen.findByRole("dialog",{name:"添加到渠道"},patience));
   expect(restored.getByLabelText("添加到 uni-api 来源")).toHaveValue("fixture");
   await waitFor(()=>expect(restored.getByLabelText("添加到 API key")).toHaveValue("second-key"),patience);
   expect(restored.getByRole("checkbox",{name:"gpt-5.5"})).toBeChecked();
  }else expect(screen.queryByRole("button",{name:"恢复添加配置"})).not.toBeInTheDocument();
  expect(posts).toBe(1);
  return;
 }
 await act(async()=>finishPost(Response.json({message:"已添加"})));
 await waitFor(()=>expect(cursors).toEqual(["","25","25"]));
 expect(document.querySelector(".sub-results-table table")).toBe(table);
 expect(table.parentElement).not.toHaveAttribute("inert");
 expect(otherAdd).toBeEnabled();
 expect(within(otherRow).getByRole("button",{name:"检测 Owned site Channel 27"})).toBeEnabled();
 expect(within(otherRow).getByRole("switch")).toBeEnabled();
 await user.click(otherAdd);
 await user.click(within(await screen.findByRole("dialog",{name:"添加到渠道"},patience)).getByRole("button",{name:"关闭添加渠道"}));
 const batchButton=screen.getByRole("button",{name:"检测全部模型 · 27 个渠道"});
 expect(batchButton).toBeEnabled();
 await user.click(batchButton);
 expect(screen.queryByRole("dialog",{name:"核对批量检测范围"})).not.toBeInTheDocument();
 await act(async()=>finish(Response.json({...readyPage(25),revision:"after-import",read_at_ms:3})));
 await screen.findByRole("dialog",{name:"核对批量检测范围"},patience);
 await waitFor(()=>expect(table.parentElement).not.toHaveAttribute("inert"));
 expect(screen.getByText("Channel 26",{selector:"strong"}).closest("tr")).toBe(row);
 expect(document.querySelector(".sub-pagination")).toHaveTextContent("2 / 2");
}, 45000);
